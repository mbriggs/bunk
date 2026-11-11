//go:build !linux

package relay

import (
	"errors"
	"io"
)

// HostCID is the vsock address of the host, seen from a guest.
const HostCID = 2

// DialVsock is only available in a Linux guest.
func DialVsock(cid, port uint32) (io.ReadWriteCloser, error) {
	return nil, errors.New("vsock: linux only")
}
