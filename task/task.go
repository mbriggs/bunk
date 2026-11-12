// Package task runs a command for a directory inside that directory's box,
// from a terminal on the host: `bunk run claude ...` in a worktree, in the
// build machine's own Rex. It copies the directory into the box, runs the
// command there over ssh with a terminal, and copies the results back.
//
// Everything is copied to the same absolute path in the box, so Claude's
// sessions (keyed by path) and git's worktree links work unchanged. Copying
// in mirrors the host: the worktree, its git repository, and Claude's session
// folders for the directory. Copying out is narrower, because the box isn't
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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/mbriggs/bunk/box"
)

// where is what a task copies, found from the directory it runs in.
type where struct {
	root   string   // the worktree's top level, or the directory outside git
	cwd    string   // where the command runs
	gitDir string   // the worktree's own git directory; "" outside git
	common string   // the repository's shared git directory
	claude []string // Claude's session folders, relative to home
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

	refs, err := w.copyIn(s)
	if err != nil {
		return 1, fmt.Errorf("copying %s into box %s: %w", w.root, b.Name, err)
	}
	status := s.interactive("cd " + quote(w.cwd) + " && exec " + quoteAll(argv))
	if err := w.copyOut(s, refs); err != nil {
		os.WriteFile(unsynced, []byte(err.Error()+"\n"), 0o600)
		return status, fmt.Errorf("copying %s back from box %s: %w", w.root, b.Name, err)
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
// branches as they were, for copyOut to check against.
func (w where) copyIn(s shell) (map[string]string, error) {
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
	// The box is the boundary, so Claude there trusts the directory instead
	// of stopping at its trust prompt. Claude asks about the git top level.
	trust := "f=~/.claude.json && jq --arg a " + quote(w.cwd) + " --arg b " + quote(w.root) +
		" '.projects[$a].hasTrustDialogAccepted = true | .projects[$b].hasTrustDialogAccepted = true' \"$f\" > \"$f.new\" && mv \"$f.new\" \"$f\""
	if _, err := s.run(trust); err != nil {
		return nil, err
	}
	refs := map[string]string{}
	if w.gitDir != "" {
		if b, err := git(w.root, "symbolic-ref", "-q", "HEAD"); err == nil {
			refs[b], _ = git(w.root, "rev-parse", "-q", "--verify", b)
		}
	}
	return refs, nil
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
			if err := s.rsync(s.host+":"+c+"/", filepath.Join(home, c)+"/"); err != nil {
				return err
			}
		}
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
