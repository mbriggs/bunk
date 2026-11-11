// punchclock runs throwaway sandbox VMs ("boxes") on a build machine, one per
// task. A box has no network device: ssh, an allowlisted HTTPS proxy, the
// model endpoint, and hallpass all reach it over vsock.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mbriggs/punchclock/box"
	"github.com/mbriggs/punchclock/clock"
	"github.com/mbriggs/punchclock/relay"
)

const usage = `usage:
  punchclock up NAME       create a box, boot it, and print its SSH host key
  punchclock rm NAME       stop a box and delete it
  punchclock ls            list boxes
  punchclock proxy NAME    connect stdin and stdout to the box's sshd (an ssh ProxyCommand)

inside a box:
  punchclock guest relay LISTEN_PATH VSOCK_PORT
  punchclock guest clock VSOCK_PORT`

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "punchclock:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd, rest := "", []string(nil)
	if len(args) > 0 {
		cmd, rest = args[0], args[1:]
	}
	switch {
	case cmd == "up" && len(rest) == 1:
		return up(rest[0])
	case cmd == "rm" && len(rest) == 1:
		b, err := box.Open(rest[0])
		if err != nil {
			return err
		}
		return b.Remove()
	case cmd == "ls" && len(rest) == 0:
		return ls()
	case cmd == "proxy" && len(rest) == 1:
		return proxy(rest[0])
	case cmd == "supervise" && len(rest) == 1:
		return supervise(rest[0])
	case cmd == "guest" && len(rest) == 3 && rest[0] == "relay":
		return guestRelay(rest[1], rest[2])
	case cmd == "guest" && len(rest) == 2 && rest[0] == "clock":
		p, err := strconv.ParseUint(rest[1], 10, 32)
		if err != nil {
			return err
		}
		clock.Follow(uint32(p))
	}
	fmt.Fprintln(os.Stderr, usage)
	os.Exit(2)
	return nil
}

// up makes sure the box exists and is running, then waits for sshd and
// prints the box's SSH host key on stdout, for known_hosts. Running it again
// converges on the same box, so a handoff that failed partway can be rerun.
// A new box's supervisor starts in its own session, so it outlives this
// command.
func up(name string) error {
	b, err := box.Open(name)
	if err != nil {
		return err
	}
	start := time.Now()
	if !b.Exists() {
		if err := b.Create(); err != nil {
			return err
		}
	}
	var exited <-chan error
	if !b.Running() {
		if exited, err = b.Start(); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ready := make(chan error, 1)
	go func() { ready <- b.WaitReady(ctx) }()
	select {
	case err := <-exited:
		return fmt.Errorf("box %s: supervisor exited (%v); see %s/supervisor.log", name, err, b.Dir)
	case err := <-ready:
		if err != nil {
			return fmt.Errorf("%w; see %s/vm.log, and punchclock rm %s", err, b.Dir, name)
		}
	}
	key, err := b.HostKey()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s: up in %v\n", name, time.Since(start).Round(10*time.Millisecond))
	fmt.Println(key)
	return nil
}

func ls() error {
	boxes, err := box.List()
	if err != nil {
		return err
	}
	for _, b := range boxes {
		state := "stopped"
		if b.Running() {
			state = "running"
		}
		fmt.Printf("%s\t%s\n", b.Name, state)
	}
	return nil
}

// proxy splices stdin and stdout to the box's sshd. name may be an ssh alias
// like t1.kant.box; the box is the part before the first dot.
func proxy(name string) error {
	name, _, _ = strings.Cut(name, ".")
	b, err := box.Open(name)
	if err != nil {
		return err
	}
	c, err := b.DialGuest(22)
	if err != nil {
		return err
	}
	defer c.Close()
	go func() {
		io.Copy(c, os.Stdin)
		c.Close()
	}()
	_, err = io.Copy(os.Stdout, c)
	return err
}

func supervise(name string) error {
	b, err := box.Open(name)
	if err != nil {
		return err
	}
	log.SetFlags(log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return b.Run(ctx)
}

// guestRelay is the guest end of a hallpass socket.
func guestRelay(listen, port string) error {
	p, err := strconv.ParseUint(port, 10, 32)
	if err != nil {
		return err
	}
	os.Remove(listen)
	ln, err := net.Listen("unix", listen)
	if err != nil {
		return err
	}
	os.Chmod(listen, 0o666)
	return relay.Guest(ln, func() (io.ReadWriteCloser, error) { return relay.DialVsock(relay.HostCID, uint32(p)) })
}
