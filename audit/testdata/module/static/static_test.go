package static

import (
	"auditfixture/library"
	"os"
	"path/filepath"
	"testing"
)

func TestStatic(t *testing.T) {
	_, _ = os.ReadFile("fixture")
	library.Read("imported fixture")
	_, _ = os.ReadFile(filepath.Join(t.TempDir(), "safe"))
}
