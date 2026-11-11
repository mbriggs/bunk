// Package box creates, runs, and removes boxes: throwaway Firecracker VMs with
// no network device. Everything a box can reach, it reaches over vsock, through
// listeners its supervisor runs on the host:
//
//	guest port 22    host -> guest ssh
//	host port 3128   the egress proxy
//	host port 8787   the model endpoint
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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Home is punchclock's directory on the build machine: $PUNCHCLOCK_HOME, or
// ~/.local/share/punchclock.
func Home() string {
	if h := os.Getenv("PUNCHCLOCK_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "punchclock")
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

// Create makes the box's directory, disk, identity drive, and VM config. It
// fails if the box already exists.
func (b Box) Create() error {
	if err := os.MkdirAll(filepath.Dir(b.Dir), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(b.Dir, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("box %s already exists", b.Name)
		}
		return err
	}
	if err := clone(filepath.Join(Home(), "image", "rootfs.ext4"), b.path("disk.ext4")); err != nil {
		os.RemoveAll(b.Dir)
		return fmt.Errorf("copying the base image: %w", err)
	}
	if err := writeIdentity(b); err != nil {
		os.RemoveAll(b.Dir)
		return err
	}
	return writeFirecrackerConfig(b)
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
	pid, err := b.pid()
	if err != nil {
		return err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return nil
	}
	return waitGone(pid)
}

// Remove stops the box and deletes it.
func (b Box) Remove() error {
	if !b.Exists() {
		return fmt.Errorf("no box %s", b.Name)
	}
	if err := b.Stop(); err != nil {
		return err
	}
	return os.RemoveAll(b.Dir)
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

func (b Box) pid() (int, error) {
	raw, err := os.ReadFile(b.path("pid"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(raw)))
}
