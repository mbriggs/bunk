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
	os.WriteFile(filepath.Join(home, "authorized_keys"), []byte("ssh-ed25519 AAAA test\n"), 0o644)
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
	if err := b.Create(); err != nil {
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
	if err := b.Create(); err == nil {
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
	if err := b.Create(); err != nil {
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
// recent one.
func TestGCRemovesOnlyOldBoxes(t *testing.T) {
	testHome(t)
	old, _ := Open("old")
	recent, _ := Open("recent")
	for _, b := range []Box{old, recent} {
		if err := b.Create(); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(b.path("supervisor.log"), []byte("started\n"), 0o600)
	}
	weekAgo := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(old.path("supervisor.log"), weekAgo, weekAgo)
	removed, err := GC(7 * 24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "old" || old.Exists() || !recent.Exists() {
		t.Fatalf("GC removed %v; old exists %v, recent exists %v", removed, old.Exists(), recent.Exists())
	}
}
