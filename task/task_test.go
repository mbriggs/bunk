package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// repo makes a repository on main with one commit and a second branch,
// other, on it, and returns its path and the commit.
func repo(t *testing.T) (root, base string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "init", "-q", "-b", "main")
	run(t, root, "git", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "base")
	run(t, root, "git", "branch", "other")
	return root, run(t, root, "git", "rev-parse", "HEAD")
}

// commitOn makes a commit on top of parent without moving any branch, as
// the box's objects arrive before its branch does.
func commitOn(t *testing.T, root, parent string) string {
	t.Helper()
	tree := run(t, root, "git", "rev-parse", parent+"^{tree}")
	return run(t, root, "git", "-c", "commit.gpgsign=false", "commit-tree", "-m", "box", "-p", parent, tree)
}

// The box's git names the branch and commit, so a box gone wrong chooses
// them. Whatever it says, no ref here is deleted, no ref but a branch is
// made, and no branch but the worktree's own moves.
func TestMoveBranchTakesNothingButABranchAndCommit(t *testing.T) {
	root, base := repo(t)
	box := commitOn(t, root, base)
	zero := strings.Repeat("0", 40)
	for _, c := range []struct{ branch, sha string }{
		{"refs/heads/other", "-d"},
		{"refs/heads/main", "-d"},
		{"-d", base},
		{"--stdin", base},
		{"refs/tags/v9", box},
		{"refs/remotes/origin/main", box},
		{"HEAD", box},
		{"refs/heads/a..b", box},
		{"refs/heads/new", "main"},
		{"refs/heads/new", ""},
		{"refs/heads/new", zero},
		{"refs/heads/other", box}, // a branch the worktree wasn't on
	} {
		w := where{root: root, branch: "refs/heads/main"}
		before, err := branches(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.moveBranch(c.branch, c.sha, before); err == nil {
			t.Errorf("moveBranch(%q, %q) went through", c.branch, c.sha)
		}
		after, _ := branches(root)
		if !reflect.DeepEqual(before, after) {
			t.Errorf("moveBranch(%q, %q) changed the branches: %v, were %v", c.branch, c.sha, after, before)
		}
		if refs := run(t, root, "git", "for-each-ref", "--format=%(refname)"); refs != "refs/heads/main\nrefs/heads/other" {
			t.Errorf("moveBranch(%q, %q) left refs %q", c.branch, c.sha, refs)
		}
	}
}

func TestMoveBranch(t *testing.T) {
	root, base := repo(t)
	box := commitOn(t, root, base)
	w := where{root: root, branch: "refs/heads/main"}
	before, err := branches(root)
	if err != nil {
		t.Fatal(err)
	}
	head := func(branch string) string { return run(t, root, "git", "rev-parse", branch) }

	// The worktree's branch moves, and a branch the box made is made.
	if err := w.moveBranch("refs/heads/main", box, before); err != nil {
		t.Fatal(err)
	}
	if err := w.moveBranch("refs/heads/new", box, before); err != nil {
		t.Fatal(err)
	}
	if head("main") != box || head("new") != box {
		t.Errorf("main is %s and new is %s, want the box's %s", head("main"), head("new"), box)
	}
	// Switching to a branch that's here, without moving it, is fine.
	if err := w.moveBranch("refs/heads/other", base, before); err != nil {
		t.Errorf("the box switched to other and left it alone: %v", err)
	}
	// A branch that moved here since copyIn stays where the host put it.
	before["refs/heads/main"] = base
	later := commitOn(t, root, base)
	if err := w.moveBranch("refs/heads/main", later, before); err == nil || !strings.Contains(err.Error(), "moved here") {
		t.Errorf("moveBranch over a branch the host moved: %v", err)
	}
	if head("main") != box {
		t.Errorf("main is %s, want it left at %s", head("main"), box)
	}
}

// A primary checkout's lanes sit inside it, and each is someone else's
// work: neither copy of the primary may carry them.
func TestFindSkipsWorktreesInsideTheRoot(t *testing.T) {
	root, _ := repo(t)
	run(t, root, "git", "worktree", "add", "-q", "-b", "lane", ".claude/worktrees/lane")
	run(t, root, "git", "worktree", "add", "-q", "-b", "odd", "a[b]*c")
	run(t, root, "git", "worktree", "add", "-q", "-b", "outside", filepath.Join(filepath.Dir(root), "outside"))
	t.Cleanup(func() { os.RemoveAll(filepath.Join(filepath.Dir(root), "outside")) })

	w, err := find(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`--exclude=/.claude/worktrees/lane/`, `--exclude=/a\[b]\*c/`}
	if got := w.skipOthers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("skipOthers() = %q, want %q", got, want)
	}

	// rsync itself agrees: a stale copy coming back leaves the lanes alone.
	stale := t.TempDir()
	os.WriteFile(filepath.Join(stale, "f"), []byte("from the box\n"), 0o644)
	for _, lane := range []string{".claude/worktrees/lane", "a[b]*c"} {
		os.WriteFile(filepath.Join(root, lane, "new"), []byte("made on the host\n"), 0o644)
	}
	args := append([]string{"-a", "--delete", "--exclude=/.git"}, w.skipOthers()...)
	if out, err := exec.Command("rsync", append(args, stale+"/", root+"/")...).CombinedOutput(); err != nil {
		t.Fatalf("rsync: %v %s", err, out)
	}
	if read(t, root, "f") != "from the box" {
		t.Error("the copy back didn't bring f")
	}
	for _, lane := range []string{".claude/worktrees/lane", "a[b]*c"} {
		if _, err := os.Stat(filepath.Join(root, lane, "new")); err != nil {
			t.Errorf("the copy back deleted %s's file: %v", lane, err)
		}
	}

	// A lane's own box skips nothing.
	lane, err := find(filepath.Join(root, ".claude/worktrees/lane"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lane.others) != 0 {
		t.Errorf("the lane skips %q", lane.others)
	}
}
