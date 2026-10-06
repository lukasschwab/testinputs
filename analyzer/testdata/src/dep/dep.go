package dep

import (
	"os"
	"path/filepath"
)

func Read(path string)        { _, _ = os.ReadFile(path) }
func Join(path string) string { return filepath.Join(path, "data") }
