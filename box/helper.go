package box

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// A box runs its own program as helpers: Start runs `PROGRAM supervise NAME`
// as the box's supervisor, and SSHConfig has ssh run `PROGRAM proxy NAME` to
// reach the guest. So every program that uses boxes hands its arguments to
// Helper first: bunk's main, and TestMain in tests that start boxes. A test
// binary that didn't would run its tests again in each helper, and start more
// boxes, so a helper is marked in its environment (helperEnv), and Start
// refuses to run in one.

// helperEnv is set in a helper's environment, to which helper it is.
const helperEnv = "BUNK_HELPER"

// Helper runs args if they're a helper's, and reports whether they were.
func Helper(args []string) (bool, error) {
	if len(args) != 2 {
		return false, nil
	}
	switch args[0] {
	case "supervise":
		return true, supervise(args[1])
	case "proxy":
		return true, proxy(args[1])
	}
	return false, nil
}

// supervise runs box name's supervisor until SIGTERM or SIGINT.
func supervise(name string) error {
	b, err := Open(name)
	if err != nil {
		return err
	}
	log.SetFlags(log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return b.Run(ctx)
}

// proxy connects stdin and stdout to box name's sshd, as ssh's ProxyCommand.
// name may be an ssh alias like t1.kant.box; the box is the part before the
// first dot.
func proxy(name string) error {
	name, _, _ = strings.Cut(name, ".")
	b, err := Open(name)
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
