package audit

import "os"

// Windows cannot forward os.Interrupt with Process.Signal. Terminate the
// child instead of swallowing the interrupt and leaving it running.
func forwardSignal(p *os.Process, s os.Signal) {
	if err := p.Signal(s); err != nil {
		_ = p.Kill()
	}
}

func forwardedSignals() []os.Signal       { return []os.Signal{os.Interrupt} }
func signalExitCode(*os.ProcessState) int { return 1 }
