//go:build !windows

package audit

import (
	"os"
	"syscall"
)

func forwardedSignals() []os.Signal            { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
func forwardSignal(p *os.Process, s os.Signal) { _ = p.Signal(s) }
func signalExitCode(state *os.ProcessState) int {
	if s, ok := state.Sys().(syscall.WaitStatus); ok && s.Signaled() {
		return 128 + int(s.Signal())
	}
	return 1
}
