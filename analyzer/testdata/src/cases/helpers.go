package cases

import (
	"io/fs"
	"os"
	"path/filepath"
)

func read(p string)          { _, _ = os.ReadFile(p) }                 // want read:"testinputs/v1"
func joined(p string) string { return filepath.Join(p, "db") }         // want joined:"testinputs/v1"
func readFS(f fs.FS)         { _, _ = fs.ReadFile(f, "fixture.txt") }  // want readFS:"testinputs/v1"
func productionOnly()        { _, _ = os.ReadFile("production-only") } // want productionOnly:"testinputs/v1"

type unrelated struct{}

func (unrelated) ReadFile(string) {}
func (unrelated) TempDir() string { return "testdata" } // want TempDir:"testinputs/v1"
