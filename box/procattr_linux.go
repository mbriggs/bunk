package box

import "syscall"

// vmProcAttr makes the VM die with its supervisor, so a killed supervisor
// can't leave a VM running with nothing serving its channels.
func vmProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
