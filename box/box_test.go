package box

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// testHome makes a bunk home with a small stand-in image and keys.
func testHome(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("BUNK_HOME", home)
	os.MkdirAll(filepath.Join(home, "image"), 0o755)
	os.WriteFile(filepath.Join(home, "image", "rootfs.ext4"), []byte("not really ext4"), 0o644)
	return home
}

// A box that exists is whole, a crashed Create's leftovers don't block the
// next one, and a second Create refuses.
func TestCreateIsWholeOrNothing(t *testing.T) {
	testHome(t)
	b, err := Open("t1")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(b.Dir+".new", 0o700) // what a crash mid-Create leaves
	os.WriteFile(filepath.Join(b.Dir+".new", "disk.ext4"), []byte("half"), 0o600)
	if err := b.Create(""); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"disk.ext4", "identity.tar", "host_key.pub"} {
		if _, err := os.Stat(b.path(f)); err != nil {
			t.Errorf("created box lacks %s: %v", f, err)
		}
	}
	if _, err := os.Stat(b.Dir + ".new"); !os.IsNotExist(err) {
		t.Errorf("%s.new is still there", b.Name)
	}
	if err := b.Create(""); err == nil {
		t.Error("a second Create of the same box succeeded")
	}
	boxes, _ := List()
	if len(boxes) != 1 || boxes[0].Name != "t1" {
		t.Errorf("List = %v, want just t1", boxes)
	}
}

// A vm.pid that names a live process that isn't a VMM (here, the test
// itself, standing in for a reused pid) is ignored, not killed.
func TestKillOrphanVMSparesOtherProcesses(t *testing.T) {
	testHome(t)
	b, _ := Open("t2")
	if err := b.Create(""); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(b.path("vm.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	if err := b.killOrphanVM(); err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(os.Getpid(), 0) != nil {
		t.Fatal("unreachable: the test process is gone")
	}
	if _, err := os.Stat(b.path("vm.pid")); !os.IsNotExist(err) {
		t.Error("the stale vm.pid is still there")
	}
}

// GC removes a stopped box unused for longer than the age, and keeps a
// recent one, one that holds work, and one that's running, which it names
// as idle.
func TestGCRemovesOnlyOldBoxes(t *testing.T) {
	testHome(t)
	old, _ := Open("old")
	recent, _ := Open("recent")
	held, _ := Open("held")
	running, _ := Open("running")
	weekAgo := time.Now().Add(-8 * 24 * time.Hour)
	for _, b := range []Box{old, recent, held, running} {
		if err := b.Create(""); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(b.path("supervisor.log"), []byte("started\n"), 0o600)
		if b != recent {
			os.Chtimes(b.path("supervisor.log"), weekAgo, weekAgo)
		}
	}
	os.WriteFile(held.path("unsynced"), []byte("the bunk run that made it was interrupted\n"), 0o600)
	// A supervisor holds its box's lock for as long as it runs.
	lock, err := os.OpenFile(running.path("lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	removed, idle, err := GC(7 * 24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "old" || old.Exists() {
		t.Errorf("GC removed %v, want only old; old exists %v", removed, old.Exists())
	}
	if len(idle) != 1 || idle[0] != "running" {
		t.Errorf("GC called %v idle, want only running", idle)
	}
	for _, b := range []Box{recent, held, running} {
		if !b.Exists() {
			t.Errorf("GC removed %s", b.Name)
		}
	}
}
