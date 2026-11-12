// Package box creates, runs, and removes boxes: throwaway VMs with no network
// device, Firecracker on Linux and vfkit on macOS. Everything a box can reach,
// it reaches over vsock, through listeners its supervisor runs on the host:
//
//	guest port 22         host -> guest ssh
//	host port 9           the guest says it's ready
//	host port 123         the host's time
//	host port 3128        the egress proxy
//	host port 8787        the model endpoint
//	host ports 5001-5004  hallpass's hallpass, gh, ssh, and git sockets
//
// Each box lives in one directory under Home()/boxes: its disk (a reflink copy
// of the base image), an identity drive with its hostname and SSH host key,
// its vsock sockets, and its logs. Removing the box removes the directory.
package box

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Home is bunk's directory on the build machine: $BUNK_HOME, or
// ~/.local/share/bunk.
func Home() string {
	if h := os.Getenv("BUNK_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "bunk")
}

// Box is one box's directory. Its zero value isn't useful; use Open.
type Box struct {
	Name string
	Dir  string
}

// A name is also the box's hostname and part of its socket paths, which
// Linux caps at 108 bytes.
var validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Open returns the box called name, which may not exist yet.
func Open(name string) (Box, error) {
	if !validName.MatchString(name) {
		return Box{}, fmt.Errorf("box name %q: use 1-32 lowercase letters, digits, and dashes, starting with a letter", name)
	}
	return Box{Name: name, Dir: filepath.Join(Home(), "boxes", name)}, nil
}

// Exists reports whether the box has been created.
func (b Box) Exists() bool {
	_, err := os.Stat(b.Dir)
	return err == nil
}

// Create makes the box's directory, disk, and identity drive, for workdir if
// it isn't "" (see ForDir). It builds them
// in NAME.new and renames that into place, so a box that exists is whole: a
// crash or a racing `up` can never boot a half-made one. It fails if the box
// already exists.
func (b Box) Create(workdir string) error {
	if err := os.MkdirAll(filepath.Dir(b.Dir), 0o755); err != nil {
		return err
	}
	if b.Exists() {
		return fmt.Errorf("box %s already exists", b.Name)
	}
	// NAME.new isn't a valid box name, so List never shows one a crash left.
	staged := Box{Name: b.Name, Dir: b.Dir + ".new"}
	os.RemoveAll(staged.Dir)
	if err := os.Mkdir(staged.Dir, 0o700); err != nil {
		return err
	}
	if err := clone(filepath.Join(Home(), "image", "rootfs.ext4"), staged.path("disk.ext4")); err != nil {
		os.RemoveAll(staged.Dir)
		return fmt.Errorf("copying the base image: %w", err)
	}
	if err := writeIdentity(staged); err != nil {
		os.RemoveAll(staged.Dir)
		return err
	}
	// The box's user is this machine's, as the image was built.
	who := os.Getenv("USER")
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	if err := os.WriteFile(staged.path("user"), []byte(who+"\n"), 0o600); err != nil {
		os.RemoveAll(staged.Dir)
		return err
	}
	if workdir != "" {
		if err := os.WriteFile(staged.path("workdir"), []byte(workdir+"\n"), 0o600); err != nil {
			os.RemoveAll(staged.Dir)
			return err
		}
	}
	if err := os.Rename(staged.Dir, b.Dir); err != nil {
		os.RemoveAll(staged.Dir)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("box %s already exists", b.Name)
		}
		return err
	}
	return nil
}

// HostKey returns the box's SSH host public key, in authorized_keys format.
func (b Box) HostKey() (string, error) {
	raw, err := os.ReadFile(b.path("host_key.pub"))
	return strings.TrimSpace(string(raw)), err
}

// Running reports whether the box's supervisor is alive: whether something
// holds the box's lock. Unlike its pid, the lock can't outlive it.
func (b Box) Running() bool {
	f, err := os.Open(b.path("lock"))
	if err != nil {
		return false
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) != nil {
		return true
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// Stop signals the supervisor, which shuts the VM down, and waits for it to
// exit. Stopping a box that isn't running does nothing; in particular, a pid
// file left by a crash isn't trusted, since the pid may have been reused.
func (b Box) Stop() error {
	if !b.Running() {
		return nil
	}
	pid, err := readPid(b.path("pid"))
	if err != nil {
		return err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stopping box %s: %w", b.Name, err)
	}
	return waitGone(pid)
}

// Remove stops the box, and any VM a dead supervisor left running on its
// disk, then deletes it.
func (b Box) Remove() error {
	if !b.Exists() {
		return fmt.Errorf("no box %s", b.Name)
	}
	if err := b.Stop(); err != nil {
		return err
	}
	if err := b.killOrphanVM(); err != nil {
		return err
	}
	return os.RemoveAll(b.Dir)
}

// killOrphanVM kills a VM whose supervisor died without taking it along. On
// macOS nothing kills vfkit with its parent, and a second VM on the same disk
// fails ("storage device attachment is invalid") or worse. The supervisor
// records the VM's pid in vm.pid; it's trusted only while that pid is still
// running a VMM, since pids get reused.
func (b Box) killOrphanVM() error {
	pid, err := readPid(b.path("vm.pid"))
	if err != nil {
		return nil
	}
	defer os.Remove(b.path("vm.pid"))
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil // not running
	}
	if name := filepath.Base(strings.TrimSpace(string(out))); name != "vfkit" && name != "firecracker" {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("killing box %s's orphaned VM %d: %w", b.Name, pid, err)
	}
	return waitGone(pid)
}

// LastUsed is when the box last did anything: when a bunk run last used it
// (see Touch), or its supervisor last logged, which it does at each start and
// at each request the box sends out.
func (b Box) LastUsed() time.Time {
	var last time.Time
	for _, f := range []string{"used", "supervisor.log"} {
		if fi, err := os.Stat(b.path(f)); err == nil && fi.ModTime().After(last) {
			last = fi.ModTime()
		}
	}
	if last.IsZero() { // never started: when it was made
		if fi, err := os.Stat(b.Dir); err == nil {
			last = fi.ModTime()
		}
	}
	return last
}

// Touch records that the box was just used.
func (b Box) Touch() { os.WriteFile(b.path("used"), nil, 0o600) }

// GC removes the stopped boxes not used for at least age, and the stopped
// boxes whose workdir is gone, and returns their names. It never touches a
// running box: one may hold work that hasn't been handed back yet.
func GC(age time.Duration) ([]string, error) {
	boxes, err := List()
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, b := range boxes {
		if b.Running() {
			continue
		}
		dirGone := false
		if w := b.Workdir(); w != "" {
			_, err := os.Stat(w)
			dirGone = os.IsNotExist(err)
		}
		if !dirGone && time.Since(b.LastUsed()) < age {
			continue
		}
		if err := b.Remove(); err != nil {
			return removed, err
		}
		removed = append(removed, b.Name)
	}
	return removed, nil
}

// List returns the boxes that exist, in name order.
func List() ([]Box, error) {
	entries, err := os.ReadDir(filepath.Join(Home(), "boxes"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var boxes []Box
	for _, e := range entries {
		if b, err := Open(e.Name()); err == nil && e.IsDir() {
			boxes = append(boxes, b)
		}
	}
	return boxes, err
}

func (b Box) path(name string) string { return filepath.Join(b.Dir, name) }

// hostSocket is where the host serves a port the guest connects out to. Both
// VMMs use the same names: Firecracker requires them, and vfkit is told them.
func (b Box) hostSocket(port uint32) string { return b.path(fmt.Sprintf("v.sock_%d", port)) }

func readPid(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(raw)))
}
