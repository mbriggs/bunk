package box

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// writeFirecrackerConfig writes the VM's config: the shared kernel, the box's
// own disk and identity drive, and a vsock device. There is no network
// interface.
func writeFirecrackerConfig(b Box) error {
	image := filepath.Join(Home(), "image")
	type drive struct {
		ID       string `json:"drive_id"`
		Path     string `json:"path_on_host"`
		Root     bool   `json:"is_root_device"`
		ReadOnly bool   `json:"is_read_only"`
	}
	cfg := map[string]any{
		"boot-source": map[string]any{
			"kernel_image_path": filepath.Join(image, "vmlinux"),
			"boot_args":         "console=ttyS0 reboot=k panic=1 root=/dev/vda rw init=/lib/systemd/systemd systemd.unified_cgroup_hierarchy=1",
		},
		"drives": []drive{
			{ID: "root", Path: b.path("disk.ext4"), Root: true},
			{ID: "identity", Path: b.path("identity.tar"), ReadOnly: true},
		},
		"machine-config": map[string]any{"vcpu_count": 4, "mem_size_mib": 8192},
		// Each VM has its own vsock device behind its own socket, so every box
		// can use the same guest CID.
		"vsock": map[string]any{"guest_cid": 3, "uds_path": b.path("v.sock")},
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(b.path("config.json"), raw, 0o600)
}

// firecracker finds the binary: Home()/bin/firecracker, then $PATH.
func firecracker() (string, error) {
	p := filepath.Join(Home(), "bin", "firecracker")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return exec.LookPath("firecracker")
}

// DialGuest connects to a port the guest listens on, through Firecracker's
// vsock socket: send "CONNECT port", read "OK hostport".
func (b Box) DialGuest(port uint32) (net.Conn, error) {
	c, err := net.Dial("unix", b.path("v.sock"))
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(c, "CONNECT %d\n", port)
	// Read the reply a byte at a time so nothing after it gets buffered here.
	var line []byte
	one := make([]byte, 1)
	for len(line) < 64 {
		if _, err := c.Read(one); err != nil {
			c.Close()
			return nil, fmt.Errorf("guest port %d: %w", port, err)
		}
		if one[0] == '\n' {
			break
		}
		line = append(line, one[0])
	}
	if !strings.HasPrefix(string(line), "OK ") {
		c.Close()
		return nil, fmt.Errorf("guest port %d: %q", port, line)
	}
	return c, nil
}

// WaitSSH waits until the guest's sshd sends its banner.
func (b Box) WaitSSH(ctx context.Context) error {
	for {
		if b.sshAnswers() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("box %s: sshd didn't answer: %w", b.Name, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (b Box) sshAnswers() bool {
	c, err := b.DialGuest(22)
	if err != nil {
		return false
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	banner, _ := bufio.NewReader(c).ReadString('\n')
	return strings.HasPrefix(banner, "SSH-")
}
