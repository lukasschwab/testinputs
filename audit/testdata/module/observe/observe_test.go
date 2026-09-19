package observe

import (
	"auditfixture/library"
	"embed"
	"os"
	"path/filepath"
	"testing"
)

//go:embed embedded.txt
var embedded embed.FS

func TestObserve(t *testing.T) {
	_, _ = os.ReadFile("fixture space.txt")
	library.Read("library fixture.txt")
	_, _ = embedded.ReadFile("embedded.txt")
	_, _ = os.Open("missing file")
	f, err := os.OpenFile("write only", os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	p := filepath.Join(t.TempDir(), "owned.txt")
	_ = os.WriteFile(p, []byte("temp"), 0600)
	_, _ = os.ReadFile(p)
	t.Chdir("sub")
	_, _ = os.ReadFile("after.txt")
}

func TestParallel(t *testing.T) {
	for _, name := range []string{"a", "b"} {
		t.Run(name, func(t *testing.T) { t.Parallel(); _, _ = os.ReadFile("parallel.txt") })
	}
}
