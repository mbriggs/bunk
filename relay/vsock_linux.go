package relay

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// HostCID is the vsock address of the host, seen from a guest.
const HostCID = 2

// DialVsock connects to a vsock port. It returns the socket as a plain file
// because net.FileConn doesn't know AF_VSOCK.
func DialVsock(cid, port uint32) (io.ReadWriteCloser, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: cid, Port: port}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("vsock %d:%d: %w", cid, port, err)
	}
	return os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d:%d", cid, port)), nil
}
