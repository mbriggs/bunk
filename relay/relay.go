// Package relay carries a Unix-socket stream across vsock without relying on
// half-close, which Firecracker turns into a full close of the host-side
// connection. The guest end frames what its client sends and marks the
// client's end of input in-band; the host end turns that marker back into
// CloseWrite on its own Unix socket. The other direction passes through as is
// and ends with a full close.
//
// hallpass's git-over-ssh protocol needs this: its wrapper half-closes after
// git's stdin, then waits for the broker's exit frame.
package relay

import (
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
)

// Guest accepts clients on ln and relays each over a fresh connection from
// dial: client -> framed -> vsock; vsock -> raw -> client.
func Guest(ln net.Listener, dial func() (io.ReadWriteCloser, error)) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer c.Close()
			up, err := dial()
			if err != nil {
				log.Printf("relay: %v", err)
				return
			}
			defer up.Close()
			go writeFrames(up, c)
			io.Copy(c, up)
		}()
	}
}

// Host accepts vsock connections on ln and relays each to the Unix socket at
// target: framed -> target, with CloseWrite at the marker; target -> raw ->
// vsock, then a full close.
func Host(ln net.Listener, target string) error {
	for {
		vs, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer vs.Close()
			t, err := net.Dial("unix", target)
			if err != nil {
				log.Printf("relay: %v", err)
				return
			}
			defer t.Close()
			go func() {
				if readFrames(t, vs) == nil {
					t.(*net.UnixConn).CloseWrite()
				}
			}()
			io.Copy(vs, t)
		}()
	}
}

// writeFrames copies r to w as [uint32 length][bytes] frames, then a
// zero-length frame for end of input.
func writeFrames(w io.Writer, r io.Reader) error {
	buf := make([]byte, 4+32<<10)
	for {
		n, err := r.Read(buf[4:])
		if n > 0 {
			binary.BigEndian.PutUint32(buf, uint32(n))
			if _, werr := w.Write(buf[:4+n]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			_, werr := w.Write([]byte{0, 0, 0, 0})
			return werr
		}
		if err != nil {
			return err
		}
	}
}

// readFrames writes the frames' bytes to w and returns nil at the
// end-of-input marker.
func readFrames(w io.Writer, r io.Reader) error {
	hdr := make([]byte, 4)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(hdr)
		if n == 0 {
			return nil
		}
		if _, err := io.CopyN(w, r, int64(n)); err != nil {
			return err
		}
	}
}
