// Package clock keeps a box's clock right. Virtualization.framework gives a
// Linux guest no real-time clock, so without this a Mac box starts in 1970
// and TLS fails; it would also drift while the laptop sleeps. The host
// answers each connection with its time; the guest asks every 30 seconds.
package clock

import (
	"fmt"
	"net"
	"time"
)

// Serve answers each connection on ln with the time in Unix nanoseconds.
func Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		fmt.Fprintf(c, "%d\n", time.Now().UnixNano())
		c.Close()
	}
}
