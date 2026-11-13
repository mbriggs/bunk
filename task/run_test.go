package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mbriggs/bunk/box"
)

// These run real boxes, so they need a build machine: KVM or vfkit, an image
// in bunk's home (BUNK_HOME), and BUNK_E2E=1. CI's boot job runs them.

// commit commits in the box without signing, since a test box may have no
// hallpass broker to sign with.
const commit = `git -c commit.gpgsign=false -c user.name=box -c user.email=box@example.com commit -qm`

// TestRunCopiesBack checks what comes back from a box: the branch, the
// working tree, and the index, but none of the rest of .git.
func TestRunCopiesBack(t *testing.T) {
	wt := worktree(t)
	status, err := Run(wt, []string{"sh", "-c", strings.Join([]string{
		"echo box > f && git add f && " + commit + " box",
		"echo untracked > g",
		`echo "exit 1" > "$(git rev-parse --git-common-dir)/hooks/pre-commit"`,
		"git config core.fromthebox yes",
		"git branch fromthebox",
		"exit 3",
	}, "; ")})
	if err != nil {
		t.Fatal(err)
	}
	if status != 3 {
		t.Errorf("status %d, want the command's 3", status)
	}
	if got := run(t, wt, "git", "log", "-1", "--format=%s", "task"); got != "box" {
		t.Errorf("task's last commit is %q, want the box's", got)
	}
	if got := read(t, wt, "f"); got != "box" {
		t.Errorf("f is %q, want the box's", got)
	}
	if got := run(t, wt, "git", "status", "--porcelain"); got != "?? g" {
		t.Errorf("git status says %q, want only the untracked g", got)
	}
	common := run(t, wt, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if _, err := os.Stat(filepath.Join(common, "hooks", "pre-commit")); err == nil {
		t.Error("the box's hook came back")
	}
	if out, _ := exec.Command("git", "-C", wt, "config", "core.fromthebox").Output(); len(out) > 0 {
		t.Error("the box's git config came back")
	}
	if exec.Command("git", "-C", wt, "rev-parse", "-q", "--verify", "refs/heads/fromthebox").Run() == nil {
		t.Error("the box's other branch came back")
	}
}

// TestRunHoldsWorkWhenBranchMoved moves the branch on the host while the box
// has it: nothing but sessions comes back, the box's commit is in the
// repository to pick up, and the box is held until it's removed.
func TestRunHoldsWorkWhenBranchMoved(t *testing.T) {
	wt := worktree(t)
	b, err := box.ForDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		status int
		err    error
	}
	done := make(chan result, 1)
	go func() {
		s, err := Run(wt, []string{"sh", "-c",
			"touch /tmp/started; while [ ! -e /tmp/go ]; do sleep 0.2; done; echo box > f && git add f && " + commit + " box"})
		done <- result{s, err}
	}()
	waitFor(t, b, "test -e /tmp/started")
	os.WriteFile(filepath.Join(wt, "f"), []byte("host\n"), 0o644)
	run(t, wt, "git", "add", "f")
	run(t, wt, "git", "-c", "commit.gpgsign=false", "commit", "-qm", "host")
	inBox(t, b, "touch /tmp/go")
	r := <-done

	if r.err == nil || !strings.Contains(r.err.Error(), "moved here") {
		t.Fatalf("Run: %v, want the branch refused", r.err)
	}
	if got := run(t, wt, "git", "log", "-1", "--format=%s", "task"); got != "host" {
		t.Errorf("task's last commit is %q, want the host's", got)
	}
	if got := read(t, wt, "f"); got != "host" {
		t.Errorf("f is %q, want the host's", got)
	}
	sha := regexp.MustCompile(`is ([0-9a-f]{12})`).FindStringSubmatch(r.err.Error())
	if sha == nil || run(t, wt, "git", "log", "-1", "--format=%s", sha[1]) != "box" {
		t.Errorf("the box's commit isn't in the repository: %v", r.err)
	}
	if _, err := Run(wt, []string{"true"}); err == nil || !strings.Contains(err.Error(), "didn't come back") {
		t.Errorf("a second Run: %v, want it refused", err)
	}
	if err := Remove(wt, false); err == nil {
		t.Error("Remove without force removed a box holding work")
	}
}

// worktree makes a repository with a linked worktree on the branch task, as
// ferry and Claude's worktrees have, and removes its box and Claude's session
// folders for it at the end.
func worktree(t *testing.T) string {
	t.Helper()
	if os.Getenv("BUNK_E2E") == "" {
		t.Skip("needs a build machine; set BUNK_E2E=1")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, wt := filepath.Join(dir, "repo"), filepath.Join(dir, "wt")
	os.Mkdir(repo, 0o755)
	run(t, repo, "git", "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "f"), []byte("base\n"), 0o644)
	run(t, repo, "git", "add", "f")
	run(t, repo, "git", "-c", "commit.gpgsign=false", "commit", "-qm", "base")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "task", wt)
	t.Cleanup(func() {
		if err := Remove(wt, true); err != nil {
			t.Errorf("removing the box: %v", err)
		}
		home, _ := os.UserHomeDir()
		os.RemoveAll(filepath.Join(home, ".claude", "projects", claudeSlug(wt)))
	})
	return wt
}

func waitFor(t *testing.T, b box.Box, check string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if cfg, err := b.SSHConfig(); err == nil && exec.Command("ssh", "-F", cfg, b.Name, check).Run() == nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("box %s: %s never held", b.Name, check)
}

func inBox(t *testing.T, b box.Box, command string) {
	t.Helper()
	cfg, err := b.SSHConfig()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("ssh", "-F", cfg, b.Name, command).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v %s", command, err, out)
	}
}

func run(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v %s", strings.Join(argv, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
