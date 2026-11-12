// bunk runs throwaway sandbox VMs ("boxes") on a build machine, one per
// task. A box has no network device: ssh, an allowlisted HTTPS proxy, the
// model endpoint, and hallpass all reach it over vsock.
package main

import (
	"context"
	"errors"
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

	"github.com/mbriggs/bunk/box"
	"github.com/mbriggs/bunk/clock"
	"github.com/mbriggs/bunk/relay"
	"github.com/mbriggs/bunk/task"
)

const usage = `usage:
  bunk up NAME       create a box, boot it, and print its SSH host key
  bunk rm NAME       stop a box and delete it
  bunk ls            list boxes, and when each was last used
  bunk gc [DAYS]     delete stopped boxes unused for DAYS days (default 7); running ones are never touched
  bunk run CMD...    run CMD for this directory in its own box: copy the directory
                     in, run CMD there on this terminal, and copy the results back
  bunk rm --dir DIR  delete DIR's box
  bunk proxy NAME    connect stdin and stdout to the box's sshd (an ssh ProxyCommand)

inside a box:
  bunk guest relay LISTEN_PATH VSOCK_PORT
  bunk guest clock VSOCK_PORT`

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "bunk:", err)
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
	case cmd == "run" && len(rest) >= 1:
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		status, err := task.Run(dir, rest)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bunk:", err)
			if status == 0 {
				status = 1
			}
		}
		os.Exit(status)
	case cmd == "rm" && len(rest) == 2 && rest[0] == "--dir":
		b, err := box.ForDir(rest[1])
		if err != nil {
			return err
		}
		if !b.Exists() {
			return nil // nothing to do: the directory never had a box
		}
		return b.Remove()
	case cmd == "rm" && len(rest) == 1:
		b, err := box.Open(rest[0])
		if err != nil {
			return err
		}
		return b.Remove()
	case cmd == "ls" && len(rest) == 0:
		return ls()
	case cmd == "gc" && len(rest) <= 1:
		days := 7
		if len(rest) == 1 {
			var err error
			if days, err = strconv.Atoi(rest[0]); err != nil || days < 0 {
				return fmt.Errorf("gc: %q isn't a number of days", rest[0])
			}
		}
		removed, err := box.GC(time.Duration(days) * 24 * time.Hour)
		for _, n := range removed {
			fmt.Println("removed", n)
		}
		return err
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
	created := false
	if !b.Exists() {
		if err := b.Create(""); err != nil {
			return err
		}
		created = true
	}
	if err := b.Up(); err != nil {
		if created && !b.Running() {
			b.Remove() // never started, so nothing in it to keep
		}
		return err
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
	running := 0
	for _, b := range boxes {
		state := "stopped"
		if b.Running() {
			state = "running"
			running++
		}
		fmt.Printf("%s\t%s\tused %s ago\n", b.Name, state, time.Since(b.LastUsed()).Round(time.Minute))
	}
	fmt.Fprintf(os.Stderr, "%d of %d running\n", running, box.MaxRunning())
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
	// When ssh finishes, stdin closes and the goroutine closes c under this
	// copy. That's the session ending, not a failure.
	if _, err := io.Copy(os.Stdout, c); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
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
