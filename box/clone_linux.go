package box

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
)

// clone copies src to dst with a reflink, which is instant on btrfs and xfs.
// On a filesystem without reflinks, such as ext4, it makes a sparse copy
// instead: the image is 12 GB but mostly holes, so that copies only the 2 GB
// or so of data, in seconds.
func clone(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	err = unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
	if err == nil {
		return out.Close()
	}
	out.Close()
	os.Remove(dst)
	if !errors.Is(err, unix.EOPNOTSUPP) && !errors.Is(err, unix.EXDEV) && !errors.Is(err, unix.EINVAL) {
		return err
	}
	if b, err := exec.Command("cp", "--sparse=always", src, dst).CombinedOutput(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("cp --sparse=always: %v %s", err, strings.TrimSpace(string(b)))
	}
	return os.Chmod(dst, 0o600)
}
