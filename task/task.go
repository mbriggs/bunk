// Package task runs a command for a directory inside that directory's box,
// from a terminal on the host: `bunk run claude ...` in a worktree, in the
// build machine's own Rex. It copies the directory into the box, runs the
// command there over ssh with a terminal, and copies the results back.
//
// Everything is copied to the same absolute path in the box, so Claude's
// sessions (keyed by path) and git's worktree links work unchanged. Copying
// in mirrors the host: the worktree, its git repository, Claude's session
// folders for the directory, and Claude's user config (claudeConfig). Copying out is narrower, because the box isn't
// trusted to touch anything beyond this worktree:
//
//   - the working tree, but not .git;
//   - git objects, only adding ones the host lacks (objects are immutable);
//   - the branch HEAD is on, moved only if the host's copy of it hasn't
//     moved since copying in;
//   - the worktree's own HEAD and index;
//   - Claude's session folders.
//
// So a box can't install git hooks or config on the host, or rewrite other
// branches.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/mbriggs/bunk/box"
)

// where is what a task copies, found from the directory it runs in.
type where struct {
	root   string   // the worktree's top level, or the directory outside git
	cwd    string   // where the command runs
	gitDir string   // the worktree's own git directory; "" outside git
	common string   // the repository's shared git directory
	claude []string // Claude's session folders, relative to home

	agent     string // "claude" or "codex" when the command runs one of them
	codexAuth []byte // the Codex login copyIn gave the box, for copyOut
}

// agent names the agent argv runs, "claude" or "codex", or "" for any other
// command. It decides which credential the box gets.
func agent(argv []string) string {
	switch n := filepath.Base(argv[0]); n {
	case "claude", "codex":
		return n
	}
	return ""
}

// Run runs argv in dir's box and returns its exit status.
func Run(dir string, argv []string) (int, error) {
	w, err := find(dir)
	if err != nil {
		return 1, err
	}
	b, err := box.ForDir(w.root)
	if err != nil {
		return 1, err
	}
	created := false
	if !b.Exists() {
		if err := b.Create(w.root); err != nil {
			return 1, err
		}
		created = true
	}
	unlock, err := lockRun(b)
	if err != nil {
		return 1, err
	}
	defer unlock()
	b.Touch()
	defer b.Touch()
	if err := b.Up(); err != nil {
		if created && !b.Running() {
			b.Remove()
		}
		return 1, err
	}
	// Copying in replaces the box's copy, so a box whose work didn't come
	// back last time is left alone until someone decides about it.
	unsynced := filepath.Join(b.Dir, "unsynced")
	if why, err := os.ReadFile(unsynced); err == nil {
		return 1, fmt.Errorf("box %s holds work that didn't come back: %s\n"+
			"Its files are still in the box (ssh -F %s %s). Once you have what you need, bunk rm --dir %s",
			b.Name, strings.TrimSpace(string(why)), filepath.Join(b.Dir, "ssh_config"), b.Name, w.root)
	}
	cfg, err := b.SSHConfig()
	if err != nil {
		return 1, err
	}
	s := shell{cfg: cfg, host: b.Name}

	// Each agent's credential is in the box only while that agent runs
	// there, so a box running anything else, or between runs, has none.
	w.agent = agent(argv)
	defer func() {
		b.TakeClaudeToken()
		s.run("rm -f ~/.codex/auth.json")
	}()
	if w.agent == "claude" {
		err = b.GiveClaudeToken()
	} else {
		err = b.TakeClaudeToken()
	}
	if err != nil {
		return 1, err
	}
	refs, err := w.copyIn(s)
	if err != nil {
		return 1, fmt.Errorf("copying %s into box %s: %w", w.root, b.Name, err)
	}
	// From here the box may hold work the host lacks, and the file says so
	// until the copy back is done. A run that dies partway writes nothing
	// more, so the box it leaves is still held.
	if err := os.WriteFile(unsynced, []byte("the bunk run that made it was interrupted\n"), 0o600); err != nil {
		return 1, err
	}
	freeze, clear := mirrorSessions(s)
	status := s.interactive("cd " + quote(w.cwd) + " && exec " + quoteAll(argv))
	// The box's Claude has gone, but its work isn't home yet: keep the
	// entries as they last were until the copy back is done.
	freeze()
	defer clear()
	if err := w.copyOut(s, refs); err != nil {
		os.WriteFile(unsynced, []byte(err.Error()+"\n"), 0o600)
		return status, fmt.Errorf("copying %s back from box %s: %w", w.root, b.Name, err)
	}
	if err := os.Remove(unsynced); err != nil {
		return status, err
	}
	return status, nil
}

// find works out what to copy for dir.
func find(dir string) (where, error) {
	// Resolved, so it agrees with the paths git reports (a Mac's /tmp is
	// /private/tmp), and names something that can exist in the box.
	cwd, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return where{}, err
	}
	if cwd, err = filepath.Abs(cwd); err != nil {
		return where{}, err
	}
	w := where{root: cwd, cwd: cwd}
	if top, err := git(cwd, "rev-parse", "--show-toplevel"); err == nil {
		w.root = top
		if w.gitDir, err = git(cwd, "rev-parse", "--path-format=absolute", "--git-dir"); err != nil {
			return where{}, err
		}
		if w.common, err = git(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir"); err != nil {
			return where{}, err
		}
	}
	for _, d := range uniq(w.cwd, w.root) {
		w.claude = append(w.claude, filepath.Join(".claude", "projects", claudeSlug(d)))
	}
	return w, nil
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// claudeSlug is the name Claude gives the session folder for a directory.
func claudeSlug(dir string) string { return nonAlnum.ReplaceAllString(dir, "-") }

// copyIn makes the box's copies match the host's, and returns the host's
// branches as they were, for copyOut to check against. It reads them before
// it copies the repository: a branch that moves here during the copy may or
// may not reach the box, and either way copyOut then finds it moved.
func (w *where) copyIn(s shell) (map[string]string, error) {
	refs := map[string]string{}
	if w.gitDir != "" {
		if b, err := git(w.root, "symbolic-ref", "-q", "HEAD"); err == nil {
			refs[b], _ = git(w.root, "rev-parse", "-q", "--verify", b)
		}
	}
	dirs := []string{w.root}
	if w.common != "" && !inside(w.common, w.root) {
		dirs = append(dirs, w.common)
	}
	// The paths may not exist in the box, or may sit under a home with
	// another name (a Mac's /Users), so they're made as root and handed over.
	var mk []string
	for _, d := range dirs {
		mk = append(mk, "sudo mkdir -p "+quote(d)+" && sudo chown \"$(id -u):$(id -g)\" "+quote(d))
	}
	for _, c := range w.claude {
		mk = append(mk, "mkdir -p "+quote(c))
	}
	if _, err := s.run(strings.Join(mk, " && ")); err != nil {
		return nil, err
	}
	for _, d := range dirs {
		if err := s.rsync(d+"/", s.host+":"+d+"/", "--delete"); err != nil {
			return nil, err
		}
	}
	home, _ := os.UserHomeDir()
	for _, c := range w.claude {
		if _, err := os.Stat(filepath.Join(home, c)); err == nil {
			if err := s.rsync(filepath.Join(home, c)+"/", s.host+":"+c+"/", "--delete"); err != nil {
				return nil, err
			}
		}
	}
	if err := copyClaudeConfig(s, home); err != nil {
		return nil, err
	}
	auth, err := w.copyCodexIn(s, home)
	if err != nil {
		return nil, err
	}
	w.codexAuth = auth
	// The box is the boundary, so Claude there trusts the directory instead
	// of stopping at its trust prompt. Claude asks about the git top level.
	trust := "f=~/.claude.json && jq --arg a " + quote(w.cwd) + " --arg b " + quote(w.root) +
		" '.projects[$a].hasTrustDialogAccepted = true | .projects[$b].hasTrustDialogAccepted = true' \"$f\" > \"$f.new\" && mv \"$f.new\" \"$f\""
	if _, err := s.run(trust); err != nil {
		return nil, err
	}
	return refs, nil
}

// claudeConfig is the user-level Claude config bunk run copies into the box,
// so Claude there has the host's instructions, settings, skills, and plugins.
// It's config, not state: no credentials, history, or other sessions. Box
// settings that matter (the model endpoint, no permission prompts) are
// managed settings in the image, which outrank these.
var claudeConfig = []string{"CLAUDE.md", "settings.json", "skills", "agents", "commands", "output-styles", "plugins"}

// copyClaudeConfig copies the host's claudeConfig, and any statusline
// script, into the box's ~/.claude. It follows symlinks, since config often
// lives in a dotfiles repository the box doesn't have. It only goes in:
// nothing the box does to it comes back.
func copyClaudeConfig(s shell, home string) error {
	names := claudeConfig
	if extra, _ := filepath.Glob(filepath.Join(home, ".claude", "statusline*")); len(extra) > 0 {
		for _, e := range extra {
			names = append(names, filepath.Base(e))
		}
	}
	for _, n := range names {
		src := filepath.Join(home, ".claude", n)
		fi, err := os.Stat(src) // follows a symlink
		if err != nil {
			continue
		}
		dst := s.host + ":.claude/" + n
		if fi.IsDir() {
			src, dst = src+"/", dst+"/"
		}
		if err := s.rsync(src, dst, "-L", "--delete"); err != nil {
			return err
		}
	}
	return nil
}

// copyOut brings back what the box may change; see the package comment.
// Claude's sessions come first, since they only add. Then git objects, which
// only add too, and the branch, which moves only if the host's copy hasn't.
// Only then the working tree, HEAD, and index, so a refused branch leaves the
// host's worktree as it was.
func (w where) copyOut(s shell, before map[string]string) error {
	home, _ := os.UserHomeDir()
	for _, c := range w.claude {
		if _, err := s.run("test -d " + quote(c)); err == nil {
			// rsync makes only the last directory, and a host where Claude
			// never ran has no ~/.claude/projects.
			if err := os.MkdirAll(filepath.Dir(filepath.Join(home, c)), 0o700); err != nil {
				return err
			}
			if err := s.rsync(s.host+":"+c+"/", filepath.Join(home, c)+"/"); err != nil {
				return err
			}
		}
	}
	if err := copyCodexOut(s, home, w.codexAuth); err != nil {
		return err
	}
	if w.gitDir != "" {
		if err := s.rsync(s.host+":"+w.common+"/objects/", w.common+"/objects/", "--ignore-existing"); err != nil {
			return err
		}
		if branch, err := s.run("git -C " + quote(w.root) + " symbolic-ref -q HEAD"); err == nil && branch != "" {
			sha, err := s.run("git -C " + quote(w.root) + " rev-parse " + quote(branch))
			if err != nil {
				return err
			}
			// The host's branch must be as it was, or absent if the box made it.
			old := before[branch]
			if old == "" {
				old = strings.Repeat("0", 40)
			}
			if _, err := git(w.root, "update-ref", branch, sha, old); err != nil {
				return fmt.Errorf("%s moved here while the box had it, so nothing came back but Claude's sessions. "+
					"The box's %s is %s, already in this repository: merge or cherry-pick it", branch, branch, sha[:12])
			}
		}
	}
	if err := s.rsync(s.host+":"+w.root+"/", w.root+"/", "--delete", "--exclude=/.git"); err != nil {
		return err
	}
	if w.gitDir != "" {
		for _, f := range []string{"HEAD", "index"} {
			if err := s.rsync(s.host+":"+filepath.Join(w.gitDir, f), filepath.Join(w.gitDir, f)); err != nil {
				return err
			}
		}
	}
	return nil
}

// mirrorSessions copies the box's Claude session registry, and its Codex
// threads' writer locks (see codexLocks), to the host's while
// the command runs, every second, with this process's pid in place of the
// box's. Tools on the host that ask whether a Claude session is running, and
// whether it's busy (ferry, before it moves a task), then get the answer for
// the box, and stopping that pid with SIGTERM ends the box's Claude, as it
// would a local one. freeze stops copying and keeps the entries; clear
// removes them. Run calls clear only after the copy back, so a session
// counts as running until its work is home.
func mirrorSessions(s shell) (freeze, clear func()) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude", "sessions")
	prefix := fmt.Sprintf("bunk-%d-", os.Getpid())
	written := map[string]bool{}
	stop := make(chan struct{})
	done := make(chan struct{})
	codex := newCodexLocks(home)
	sync := func() {
		// One ssh round trip a second: Claude's registry, as "C json", and the
		// Codex threads whose writer locks are held, as "X id" (lslocks reads
		// locks without taking them, so it can't trip a Codex starting up).
		out, err := s.run(`{ cat ~/.claude/sessions/*.json 2>/dev/null | jq -c 'select(.sessionId) | {sessionId, status}' | sed 's/^/C /'; ` +
			`lslocks -n -o PATH 2>/dev/null | grep '/.codex/thread-writer-locks/.*\.lock$' | sed 's|.*/||; s|\.lock$||; s/^/X /'; } 2>/dev/null`)
		if err != nil {
			return
		}
		seen := map[string]bool{}
		threads := map[string]bool{}
		for _, line := range strings.Split(out, "\n") {
			if id, ok := strings.CutPrefix(line, "X "); ok {
				threads[id] = true
				continue
			}
			line = strings.TrimPrefix(line, "C ")
			var e struct {
				SessionID string `json:"sessionId"`
				Status    string `json:"status"`
			}
			if json.Unmarshal([]byte(line), &e) != nil || e.SessionID == "" || strings.ContainsAny(e.SessionID, "/.") {
				continue
			}
			raw, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": e.SessionID, "status": e.Status, "via": "bunk"})
			os.MkdirAll(dir, 0o700)
			name := filepath.Join(dir, prefix+e.SessionID+".json")
			os.WriteFile(name, raw, 0o600)
			written[name], seen[name] = true, true
		}
		for name := range written {
			if !seen[name] {
				os.Remove(name)
				delete(written, name)
			}
		}
		codex.hold(threads)
	}
	go func() {
		defer close(done)
		for {
			sync()
			select {
			case <-stop:
				return
			case <-time.After(time.Second):
			}
		}
	}()
	freeze = func() {
		close(stop)
		<-done
	}
	clear = func() {
		for name := range written {
			os.Remove(name)
		}
		codex.release()
	}
	return freeze, clear
}

// Remove deletes dir's box, refusing while a bunk run is using it or when the
// box holds work that didn't come back, unless force. A directory without a
// box is fine: there's nothing to do.
func Remove(dir string, force bool) error {
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		abs = dir // the directory may be gone already; its box isn't
	}
	b, err := box.ForDir(abs)
	if err != nil {
		return err
	}
	if !b.Exists() {
		return nil
	}
	if !force {
		unlock, err := lockRun(b)
		if err != nil {
			return fmt.Errorf("%w; it's still running, so it may hold work (bunk rm --dir --force removes it anyway)", err)
		}
		defer unlock()
		if why, err := os.ReadFile(filepath.Join(b.Dir, "unsynced")); err == nil {
			return fmt.Errorf("box %s holds work that didn't come back: %s (bunk rm --dir --force removes it anyway)", b.Name, strings.TrimSpace(string(why)))
		}
	}
	return b.Remove()
}

// lockRun keeps two `bunk run`s from copying one box at once.
func lockRun(b box.Box) (func(), error) {
	f, err := os.OpenFile(filepath.Join(b.Dir, "run.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another bunk run is using box %s (for %s)", b.Name, b.Workdir())
	}
	return func() { f.Close() }, nil
}

// shell runs commands in the box over ssh.
type shell struct{ cfg, host string }

func (s shell) ssh(extra ...string) []string {
	return append([]string{"ssh", "-F", s.cfg}, extra...)
}

// run runs a command in the box and returns its trimmed output.
func (s shell) run(command string) (string, error) {
	argv := s.ssh(s.host, command)
	cmd := exec.Command(argv[0], argv[1:]...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %v %s", command, err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (s shell) rsync(src, dst string, flags ...string) error {
	argv := append([]string{"-a", "-e", "ssh -F " + quote(s.cfg)}, flags...)
	cmd := exec.Command("rsync", append(argv, src, dst)...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rsync %s %s: %v %s", src, dst, err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// interactive runs a command in the box on this terminal and returns its exit
// status. A SIGTERM or SIGHUP here (ferry stopping the agent, or its pane
// closing) is passed to ssh, which ends the remote command, and Run goes on
// to copy back.
func (s shell) interactive(command string) int {
	argv := s.ssh("-t", s.host, command)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "bunk:", err)
		return 1
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigs:
				if sig != syscall.SIGINT { // ^C belongs to the remote command
					cmd.Process.Signal(syscall.SIGTERM)
				}
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func inside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && !strings.HasPrefix(rel, "..")
}

func uniq(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if len(out) == 0 || out[len(out)-1] != x {
			out = append(out, x)
		}
	}
	return out
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func quoteAll(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = quote(a)
	}
	return strings.Join(q, " ")
}
