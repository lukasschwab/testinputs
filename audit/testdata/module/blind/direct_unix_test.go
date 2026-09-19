//go:build !windows

package blind

import "syscall"

func directSyscall() {
	fd, err := syscall.Open("syscall-unlogged", syscall.O_RDONLY, 0)
	if err == nil {
		syscall.Close(fd)
	}
}
