package task

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/mbriggs/bunk/box"
)

// Codex gets the treatment Claude does. bunk run copies the host's Codex config
// in (codexConfig), and Codex's sessions in and back, so `codex resume`
// finds them. It copies bunk's own Codex login in (see box.CodexLogin) and
// keeps the box's copy if Codex refreshed it there. And it holds each thread's
// writer lock on the host while the box's Codex holds it, which is how ferry
// tells a Codex thread is in use.

// codexConfig is the user-level Codex config copied into the box. Approvals
// and Codex's own sandbox are set by the box's managed config, which outranks
// it.
var codexConfig = []string{"AGENTS.md", "config.toml", "hooks.json", "rules", "skills", "prompts", "plugins"}

// copyCodexIn copies Codex's config, sessions, and login into the box, marks
// the task's directories trusted there, and returns the login it copied.
func (w where) copyCodexIn(s shell, home string) ([]byte, error) {
	if _, err := s.run("mkdir -p ~/.codex/sessions"); err != nil {
		return nil, err
	}
	for _, n := range codexConfig {
		src := filepath.Join(home, ".codex", n)
		fi, err := os.Stat(src) // follows a symlink into a dotfiles repository
		if err != nil {
			continue
		}
		dst := s.host + ":.codex/" + n
		if fi.IsDir() {
			src, dst = src+"/", dst+"/"
		}
		if err := s.rsync(src, dst, "-L", "--delete"); err != nil {
			return nil, err
		}
	}
	if sessions := filepath.Join(home, ".codex", "sessions"); isDir(sessions) {
		if err := s.rsync(sessions+"/", s.host+":.codex/sessions/"); err != nil {
			return nil, err
		}
	}
	// The box's own copy of config.toml, so trusting the directory here
	// changes nothing on the host.
	var trust strings.Builder
	for _, d := range uniq(w.cwd, w.root) {
		fmt.Fprintf(&trust, "\n[projects.%s]\ntrust_level = \"trusted\"\n", strconv.Quote(d))
	}
	trust.WriteString(hookTrust(home))
	if err := s.pipe("cat >> ~/.codex/config.toml", trust.String()); err != nil {
		return nil, err
	}
	auth, err := box.LoadSecret("codex-auth")
	if err != nil || auth == nil {
		return nil, err // no login: Codex in the box isn't signed in
	}
	if err := s.pipe("umask 077 && cat > ~/.codex/auth.json", string(auth)); err != nil {
		return nil, err
	}
	return auth, nil
}

// copyCodexOut copies Codex's sessions back, and keeps the box's login if
// Codex refreshed it there: the refresh may have retired the copy stored.
func copyCodexOut(s shell, home string, auth []byte) error {
	if _, err := s.run("test -d ~/.codex/sessions"); err == nil {
		os.MkdirAll(filepath.Join(home, ".codex", "sessions"), 0o700)
		if err := s.rsync(s.host+":.codex/sessions/", filepath.Join(home, ".codex", "sessions")+"/"); err != nil {
			return err
		}
	}
	if auth == nil {
		return nil
	}
	now, err := s.run("cat ~/.codex/auth.json")
	if err != nil || now == strings.TrimSpace(string(auth)) {
		return nil
	}
	was, _ := box.CodexRefreshed(auth)
	is, err := box.CodexRefreshed([]byte(now))
	if err != nil || is <= was { // RFC 3339 in UTC sorts as text
		return nil
	}
	return box.SaveSecret("codex-auth", []byte(now))
}

// trustedHook is one hook config.toml trusts: the hook's place in its
// hooks.json (the key after the path) and the hash Codex trusted.
var trustedHook = regexp.MustCompile(`(?m)^\[hooks\.state\."(.*?)/\.codex/hooks\.json:([^"]*)"\]\s*\ntrusted_hash = "([^"]*)"`)

// hookTrust carries the host's hook trust over to the box's hooks.json.
// Codex records it per path (hooks.state."PATH:EVENT:N:M"), and config.toml
// often comes from a dotfiles repository whose entries name another
// machine's home, so the box's Codex would stop to ask about hooks it was
// told to trust. The hash covers the hook, not the path, so the same hook in
// the box's hooks.json gets the same hash, at the box's path (its home is
// the host's).
func hookTrust(home string) string {
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		return ""
	}
	cfg := string(raw)
	var out strings.Builder
	added := map[string]bool{}
	for _, m := range trustedHook.FindAllStringSubmatch(cfg, -1) {
		key := home + "/.codex/hooks.json:" + m[2]
		if added[key] || strings.Contains(cfg, strconv.Quote(key)) {
			continue
		}
		added[key] = true
		fmt.Fprintf(&out, "\n[hooks.state.%s]\ntrusted_hash = %s\n", strconv.Quote(key), strconv.Quote(m[3]))
	}
	return out.String()
}

// codexLocks holds, on the host, the writer locks of the Codex threads the
// box's Codex has open.
type codexLocks struct {
	dir  string
	held map[string]*os.File
}

func newCodexLocks(home string) *codexLocks {
	return &codexLocks{dir: filepath.Join(home, ".codex", "thread-writer-locks"), held: map[string]*os.File{}}
}

// hold makes the host's locks match ids, the threads locked in the box.
func (c *codexLocks) hold(ids map[string]bool) {
	for id := range ids {
		if c.held[id] != nil || strings.ContainsAny(id, "/.") {
			continue
		}
		os.MkdirAll(c.dir, 0o700)
		f, err := os.OpenFile(filepath.Join(c.dir, id+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			continue
		}
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			f.Close() // something here has it already
			continue
		}
		c.held[id] = f
	}
	for id, f := range c.held {
		if !ids[id] {
			f.Close()
			delete(c.held, id)
		}
	}
}

func (c *codexLocks) release() { c.hold(nil) }

// pipe runs command in the box with input on its stdin, so a secret is in no
// command line.
func (s shell) pipe(command, input string) error {
	argv := s.ssh(s.host, command)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(input)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v %s", command, err, strings.TrimSpace(errb.String()))
	}
	return nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
