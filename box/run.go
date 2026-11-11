package box

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/mbriggs/bunk/clock"
	"github.com/mbriggs/bunk/egress"
	"github.com/mbriggs/bunk/modelproxy"
	"github.com/mbriggs/bunk/relay"
)

// Every box gets the same machine: Firecracker and vfkit only touch the
// memory a guest uses, so the ceiling costs nothing until a task needs it.
const (
	cpus      = 4
	memoryMiB = 8192
)

// The vsock ports a box's guest connects out to.
const (
	portReady = 9    // the guest connects once sshd is up
	portClock = 123  // the host's time
	portProxy = 3128 // the egress proxy
	portModel = 8787 // the model endpoint
)

// hallpassPorts carry hallpass's sockets. Boxes share the build machine
// user's hallpass, so the broker sees this user's uid.
var hallpassPorts = map[string]uint32{"hallpass": 5001, "gh": 5002, "ssh": 5003, "git": 5004}

// channel is one port the guest connects out to, and what serves it.
type channel struct {
	port  uint32
	serve func(net.Listener) error
}

func (b Box) channels() []channel {
	model := modelproxy.Handler(credentials())
	policy := egress.Policy{AllowFile: filepath.Join(Home(), "allow.txt"), ModeFile: filepath.Join(Home(), "mode")}
	cs := []channel{
		{portReady, b.serveReady},
		{portClock, clock.Serve},
		{portProxy, func(ln net.Listener) error { return egress.Serve(ln, policy) }},
		{portModel, func(ln net.Listener) error { return http.Serve(ln, model) }},
	}
	for name, port := range hallpassPorts {
		target := filepath.Join(hallpassDir, name+".sock")
		cs = append(cs, channel{port, func(ln net.Listener) error { return relay.Host(ln, target) }})
	}
	return cs
}

// Run is the box's supervisor: it serves the host side of every vsock channel
// and runs the VM until the VM exits or ctx is cancelled. A box has at most
// one supervisor, which holds the box's lock file; its pid is in the box's
// directory.
func (b Box) Run(ctx context.Context) error {
	// The lock is held for the supervisor's life, so two `up`s racing on one
	// box can't start two VMs on one disk.
	lock, err := os.OpenFile(b.path("lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("box %s is already running", b.Name)
	}
	if err := os.WriteFile(b.path("pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(b.path("pid"))
	b.clearRunState()
	defer b.clearRunState()
	if err := b.killOrphanVM(); err != nil {
		return err
	}
	// The config is derived from the box's directory, so it's written fresh
	// on each start rather than kept.
	if err := b.writeVMConfig(); err != nil {
		return err
	}

	var ports []uint32
	for _, c := range b.channels() {
		ln, err := net.Listen("unix", b.hostSocket(c.port))
		if err != nil {
			return err
		}
		defer ln.Close()
		go func() {
			if err := c.serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
				log.Printf("port %d: %v", c.port, err)
			}
		}()
		ports = append(ports, c.port)
	}

	cmd, err := b.vmCommand(ports)
	if err != nil {
		return err
	}
	console, err := os.OpenFile(b.path("vm.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer console.Close()
	cmd.Stdout, cmd.Stderr = console, console
	if err := cmd.Start(); err != nil {
		return err
	}
	log.Printf("box %s: VM pid %d", b.Name, cmd.Process.Pid)
	if err := os.WriteFile(b.path("vm.pid"), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		cmd.Process.Kill()
		return err
	}
	defer os.Remove(b.path("vm.pid"))
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		return fmt.Errorf("the VM exited: %v", err)
	case <-ctx.Done():
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-exited
		}
		log.Printf("box %s: stopped", b.Name)
		return nil
	}
}

// Start runs this binary's `supervise NAME` for the box in its own session,
// so it outlives the caller, logging to supervisor.log. The channel receives
// its exit. A ready file left by a supervisor that died is removed first, so
// WaitReady sees this boot's.
func (b Box) Start() (<-chan error, error) {
	os.Remove(b.path("ready"))
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(b.path("supervisor.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer logf.Close()
	sup := exec.Command(self, "supervise", b.Name)
	sup.Stdout, sup.Stderr = logf, logf
	sup.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := sup.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- sup.Wait() }()
	return exited, nil
}

// serveReady marks the box ready when the guest connects, which it does once
// sshd is up. Dialing the guest's sshd to find out instead can wedge vfkit's
// forwarder if it comes too early.
func (b Box) serveReady(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		c.Close()
		if err := os.WriteFile(b.path("ready"), nil, 0o600); err != nil {
			return err
		}
	}
}

// WaitReady waits until the guest has said it's ready.
func (b Box) WaitReady(ctx context.Context) error {
	for {
		if _, err := os.Stat(b.path("ready")); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("box %s didn't come up: %w", b.Name, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// clearRunState removes what only means something while the VM runs.
func (b Box) clearRunState() {
	os.Remove(b.path("ready"))
	matches, _ := filepath.Glob(b.path("v.sock*"))
	for _, m := range matches {
		os.Remove(m)
	}
}

// credentials is where this machine's Claude keeps its OAuth token.
func credentials() modelproxy.Credentials {
	if runtime.GOOS == "darwin" {
		return modelproxy.Credentials{Cmd: "security find-generic-password -s 'Claude Code-credentials' -w"}
	}
	home, _ := os.UserHomeDir()
	return modelproxy.Credentials{Path: filepath.Join(home, ".claude", ".credentials.json")}
}

// waitGone waits for pid to exit.
func waitGone(pid int) error {
	for range 150 {
		if syscall.Kill(pid, 0) != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("supervisor %d still running after 15s", pid)
}
