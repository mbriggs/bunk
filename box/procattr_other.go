//go:build !linux

package box

import "syscall"

func vmProcAttr() *syscall.SysProcAttr { return nil }
