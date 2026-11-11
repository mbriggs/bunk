package box

import (
	"os"

	"golang.org/x/sys/unix"
)

// clone copies src to dst with a reflink, which is instant on btrfs and xfs.
// It fails rather than falling back to a full copy of a 12 GB image.
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
	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil {
		os.Remove(dst)
		return err
	}
	return out.Close()
}
