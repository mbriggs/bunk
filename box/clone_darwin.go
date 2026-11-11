package box

import "golang.org/x/sys/unix"

// clone copies src to dst with an APFS clone, which is instant.
func clone(src, dst string) error {
	return unix.Clonefile(src, dst, 0)
}
