package box

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
)

// On macOS a box is a vfkit (Virtualization.framework) VM, booted from
// Home()/image/Image with an empty initrd, which vfkit requires. vfkit
// declares each vsock port as its own device: the guest's sshd at v.sock (the
// host connects in), and each port the guest connects out to at v.sock_P.
// Virtualization.framework gives the guest no clock, so it follows the host's
// over vsock (see the clock package).
//
// Unlike Firecracker on Linux, nothing kills vfkit if its supervisor dies.

const hallpassDir = "/var/db/hallpass/run"

func (b Box) writeVMConfig() error { return nil }

// vmCommand returns the command that runs the VM, with a vsock device for
// each of ports.
func (b Box) vmCommand(ports []uint32) (*exec.Cmd, error) {
	vfkit, err := exec.LookPath("vfkit")
	if err != nil {
		return nil, err
	}
	image := filepath.Join(Home(), "image")
	args := []string{
		"--cpus", strconv.Itoa(cpus), "--memory", strconv.Itoa(memoryMiB),
		"--bootloader", fmt.Sprintf("linux,kernel=%s,initrd=%s,cmdline=console=hvc0 root=/dev/vda rw init=/lib/systemd/systemd",
			filepath.Join(image, "Image"), filepath.Join(image, "initrd.cpio")),
		"--device", "virtio-blk,path=" + b.path("disk.ext4"),
		"--device", "virtio-blk,path=" + b.path("identity.tar"),
		"--device", "virtio-rng",
		"--device", "virtio-serial,logFilePath=" + b.path("console.log"),
		"--device", "virtio-vsock,port=22,socketURL=" + b.path("v.sock") + ",connect",
	}
	for _, p := range ports {
		args = append(args, "--device", fmt.Sprintf("virtio-vsock,port=%d,socketURL=%s,listen", p, b.hostSocket(p)))
	}
	return exec.Command(vfkit, args...), nil
}

// DialGuest connects to the guest's sshd, the one port the host dials.
func (b Box) DialGuest(port uint32) (net.Conn, error) {
	if port != 22 {
		return nil, fmt.Errorf("guest port %d: only 22 is reachable on macOS", port)
	}
	return net.Dial("unix", b.path("v.sock"))
}
