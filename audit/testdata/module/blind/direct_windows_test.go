package blind

import "syscall"

func directSyscall() {
	p, _ := syscall.UTF16PtrFromString("syscall-unlogged")
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, 0, 0)
	if err == nil {
		syscall.CloseHandle(h)
	}
}
