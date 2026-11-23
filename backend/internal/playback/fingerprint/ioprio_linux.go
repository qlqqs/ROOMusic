//go:build linux

package fingerprint

import "syscall"

func setIdleIOPriority(pid int) {
	const ioPriorityClassIdle = 3 << 13
	_, _, _ = syscall.Syscall(syscall.SYS_IOPRIO_SET, 1, uintptr(pid), ioPriorityClassIdle)
}
