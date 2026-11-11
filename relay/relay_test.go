package relay

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// A client that half-closes must get the reply its target sends after seeing
// end of input, even though the hop between Guest and Host never carries a
// half-close.
func TestEndOfInputCrossesTheRelay(t *testing.T) {
	dir := shortTempDir(t)
	target := listen(t, filepath.Join(dir, "target.sock"))
	go func() {
		c, err := target.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		n, _ := io.Copy(io.Discard, c)
		fmt.Fprintf(c, "got %d", n)
	}()
	hostLn := listen(t, filepath.Join(dir, "host.sock"))
	go Host(hostLn, filepath.Join(dir, "target.sock"))
	guestLn := listen(t, filepath.Join(dir, "guest.sock"))
	go Guest(guestLn, func() (io.ReadWriteCloser, error) {
		c, err := net.Dial("unix", filepath.Join(dir, "host.sock"))
		return fullCloseOnly{c}, err
	})

	c, err := net.Dial("unix", filepath.Join(dir, "guest.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	payload := bytes.Repeat([]byte("0032want deadbeef\n"), 5000)
	if _, err := c.Write(payload); err != nil {
		t.Fatal(err)
	}
	c.(*net.UnixConn).CloseWrite()
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("got %d", len(payload)); string(got) != want {
		t.Fatalf("reply %q, want %q", got, want)
	}
}

// The host end must see exactly the client's bytes, however the stream is
// chunked.
func TestFramesSurviveOneByteReads(t *testing.T) {
	payload := bytes.Repeat([]byte("abc"), 20000)
	var wire bytes.Buffer
	if err := writeFrames(&wire, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := readFrames(&got, oneByte{&wire}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("got %d bytes, want %d", got.Len(), len(payload))
	}
}

// shortTempDir stays under macOS's 104-byte limit on socket paths.
func shortTempDir(t *testing.T) string {
	dir, err := os.MkdirTemp("/tmp", "relay")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func listen(t *testing.T, path string) net.Listener {
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

// fullCloseOnly hides CloseWrite, the way Firecracker's vsock does.
type fullCloseOnly struct{ io.ReadWriteCloser }

type oneByte struct{ r io.Reader }

func (o oneByte) Read(p []byte) (int, error) { return o.r.Read(p[:1]) }
