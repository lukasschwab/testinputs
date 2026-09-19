package blind

import (
	"os"
	"os/exec"
	"testing"
)

func init()                 { _, _ = os.ReadFile("init-unlogged") }
func TestMain(m *testing.M) { _, _ = os.ReadFile("before-m-run-unlogged"); os.Exit(m.Run()) }
func TestChild(t *testing.T) {
	if os.Getenv("AUDIT_CHILD") == "1" {
		_, _ = os.ReadFile("child-unlogged")
	}
}
func TestBlind(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestChild$")
	cmd.Env = append(os.Environ(), "AUDIT_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	directSyscall()
}
