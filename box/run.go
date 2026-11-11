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

	"github.com/mbriggs/punchclock/egress"
	"github.com/mbriggs/punchclock/modelproxy"
	"github.com/mbriggs/punchclock/relay"
)

// hallpassPorts are the vsock ports that carry hallpass's sockets. Boxes
// share the build machine user's hallpass, so the broker sees this user's uid.
var hallpassPorts = map[string]uint32{"hallpass": 5001, "gh": 5002, "ssh": 5003, "git": 5004}

const hallpassDir = "/run/hallpass"

// Run is the box's supervisor: it serves the host side of every vsock channel
// and runs the VM until the VM exits or ctx is cancelled. A box has exactly
// one supervisor; its pid is in the box's directory.
func (b Box) Run(ctx context.Context) error {
	if b.Running() {
		return fmt.Errorf("box %s is already running", b.Name)
	}
	fc, err := firecracker()
	if err != nil {
		return err
	}
	if err := os.WriteFile(b.path("pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(b.path("pid"))
	b.removeSockets()
	defer b.removeSockets()

	listen := func(port uint32) (net.Listener, error) {
		return net.Listen("unix", b.path(fmt.Sprintf("v.sock_%d", port)))
	}
	var lns []net.Listener
	defer func() {
		for _, ln := range lns {
			ln.Close()
		}
	}()
	serve := func(port uint32, f func(net.Listener) error) error {
		ln, err := listen(port)
		if err != nil {
			return err
		}
		lns = append(lns, ln)
		go func() {
			if err := f(ln); err != nil && !errors.Is(err, net.ErrClosed) {
				log.Printf("port %d: %v", port, err)
			}
		}()
		return nil
	}
	model := modelproxy.Handler(credentials())
	policy := egress.Policy{AllowFile: filepath.Join(Home(), "allow.txt"), ModeFile: filepath.Join(Home(), "mode")}
	if err := serve(8787, func(ln net.Listener) error { return http.Serve(ln, model) }); err != nil {
		return err
	}
	if err := serve(3128, func(ln net.Listener) error { return egress.Serve(ln, policy) }); err != nil {
		return err
	}
	for name, port := range hallpassPorts {
		target := filepath.Join(hallpassDir, name+".sock")
		if err := serve(port, func(ln net.Listener) error { return relay.Host(ln, target) }); err != nil {
			return err
		}
	}

	console, err := os.OpenFile(b.path("console.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer console.Close()
	cmd := exec.Command(fc, "--no-api", "--config-file", b.path("config.json"))
	cmd.Stdout, cmd.Stderr = console, console
	if err := cmd.Start(); err != nil {
		return err
	}
	log.Printf("box %s: firecracker pid %d", b.Name, cmd.Process.Pid)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		return fmt.Errorf("firecracker exited: %v", err)
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

func (b Box) removeSockets() {
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
