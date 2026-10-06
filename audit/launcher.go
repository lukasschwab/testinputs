package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
)

// Launch is the internal -exec entry point. Initial metadata makes interrupted
// invocations visible even if the launcher cannot write its completion record.
func Launch(args []string) int {
	dir := os.Getenv(launcherEnv)
	if dir == "" || len(args) == 0 {
		fmt.Fprintln(os.Stderr, "testfs: launcher requires an audit session and test binary")
		return ExitAuditFailure
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitAuditFailure
	}
	var manifest struct {
		Packages []Package
		TempBase string
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitAuditFailure
	}
	base, err := os.MkdirTemp(dir, "run-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitAuditFailure
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitAuditFailure
	}
	inv := Invocation{CWD: cwd, Args: args, ExitCode: -1}
	for _, p := range manifest.Packages {
		if sameDir(p.Dir, cwd) {
			if inv.Package != "" {
				inv.Error = "ambiguous package directory"
				break
			}
			inv.Package = p.ImportPath
		}
	}
	if inv.Package == "" {
		inv.Error = "working directory does not match package metadata"
	}
	meta := filepath.Join(base, "invocation.json")
	if err = writeJSON(meta, inv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitAuditFailure
	}
	if inv.Error != "" {
		fmt.Fprintln(os.Stderr, inv.Error)
		return ExitAuditFailure
	}
	for _, a := range args[1:] {
		name, _, _ := flagName(a)
		if name == "testlogfile" {
			fmt.Fprintln(os.Stderr, "testfs: conflicting test log flag")
			return ExitAuditFailure
		}
	}
	childArgs := append([]string{"-test.testlogfile=" + filepath.Join(base, "actions.log")}, args[1:]...)
	cmd := exec.Command(args[0], childArgs...)
	// The audit test harness uses an environment-gated dispatcher. Its child is
	// the package test binary, not another dispatcher invocation.
	cmd.Env = append(os.Environ(), auditHelperEnv+"=0")
	if manifest.TempBase != "" {
		cmd.Env = append(cmd.Env, "TMPDIR="+manifest.TempBase, "TMP="+manifest.TempBase, "TEMP="+manifest.TempBase)
	}
	inv.Started = true
	if err = writeJSON(meta, inv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitAuditFailure
	}
	inv.ExitCode = runProcess(cmd)
	inv.Finished = true
	if err = writeJSON(meta, inv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if inv.ExitCode == 0 {
			return ExitAuditFailure
		}
	}
	return inv.ExitCode
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	x, errA := os.Stat(a)
	y, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(x, y)
}

func runProcess(cmd *exec.Cmd) int {
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "testfs:", err)
		return ExitAuditFailure
	}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, forwardedSignals()...)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case s := <-signals:
				forwardSignal(cmd.Process, s)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	signal.Stop(signals)
	close(done)
	<-stopped
	if err == nil {
		return 0
	}
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		if code >= 0 {
			return code
		}
		return signalExitCode(cmd.ProcessState)
	}
	return ExitAuditFailure
}
