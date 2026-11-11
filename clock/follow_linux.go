package clock

import (
	"fmt"
	"log"
	"time"

	"github.com/mbriggs/bunk/relay"
	"golang.org/x/sys/unix"
)

// Follow sets the guest's clock from the host's vsock port, at start and
// every 30 seconds. It logs only when it moves the clock by over a second.
func Follow(port uint32) {
	for {
		if err := syncOnce(port); err != nil {
			log.Printf("clock: %v", err)
		}
		time.Sleep(30 * time.Second)
	}
}

func syncOnce(port uint32) error {
	c, err := relay.DialVsock(relay.HostCID, port)
	if err != nil {
		return err
	}
	defer c.Close()
	var ns int64
	if _, err := fmt.Fscan(c, &ns); err != nil {
		return err
	}
	off := time.Until(time.Unix(0, ns))
	if off > -time.Second && off < time.Second {
		return nil
	}
	tv := unix.NsecToTimeval(ns)
	if err := unix.Settimeofday(&tv); err != nil {
		return err
	}
	log.Printf("clock: moved by %v", off.Round(time.Millisecond))
	return nil
}
