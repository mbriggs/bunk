package box

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// On Linux a box is a Firecracker VM, booted from Home()/image/vmlinux.
// Firecracker gives each VM one vsock socket, v.sock: the host dials guest
// ports through it, and a guest connection to port P arrives at v.sock_P.

const hallpassDir = "/run/hallpass"

// writeVMConfig writes the VM's config: the kernel, the box's own disk and
// identity drive, and a vsock device. There is no network interface.
func (b Box) writeVMConfig() error {
	type drive struct {
		ID       string `json:"drive_id"`
		Path     string `json:"path_on_host"`
		Root     bool   `json:"is_root_device"`
		ReadOnly bool   `json:"is_read_only"`
	}
	cfg := map[string]any{
		"boot-source": map[string]any{
			"kernel_image_path": filepath.Join(Home(), "image", "vmlinux"),
			"boot_args":         "console=ttyS0 reboot=k panic=1 root=/dev/vda rw init=/lib/systemd/systemd systemd.unified_cgroup_hierarchy=1",
		},
		"drives": []drive{
			{ID: "root", Path: b.path("disk.ext4"), Root: true},
			{ID: "identity", Path: b.path("identity.tar"), ReadOnly: true},
		},
		"machine-config": map[string]any{"vcpu_count": cpus, "mem_size_mib": memoryMiB},
		// Each VM has its own vsock device behind its own socket, so every
		// box can use the same guest CID.
		"vsock": map[string]any{"guest_cid": 3, "uds_path": b.path("v.sock")},
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(b.path("config.json"), raw, 0o600)
}

// vmCommand returns the command that runs the VM. Firecracker forwards every
// guest port, so ports isn't needed. The VM gets SIGKILL if its supervisor
// dies, so it can't run on with nothing serving its channels.
func (b Box) vmCommand(ports []uint32) (*exec.Cmd, error) {
	fc := filepath.Join(Home(), "bin", "firecracker")
	if _, err := os.Stat(fc); err != nil {
		if fc, err = exec.LookPath("firecracker"); err != nil {
			return nil, err
		}
	}
	cmd := exec.Command(fc, "--no-api", "--config-file", b.path("config.json"))
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return cmd, nil
}

// DialGuest connects to a port the guest listens on: send "CONNECT port" on
// v.sock, read "OK hostport".
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
