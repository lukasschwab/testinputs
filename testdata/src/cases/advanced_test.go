package cases

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

type wrapper struct{ FS fs.FS }

func (w wrapper) Open(name string) (fs.File, error) { return w.FS.Open(name) }

type diskWrapper struct{ FS fs.FS }

func (w diskWrapper) Open(name string) (fs.File, error) { return os.Open("wrapper-disk") }

type alias = embed.FS

func TestWrappers(t *testing.T) {
	var a alias = fixtures
	_, _ = fs.ReadFile(a, "fixture.txt")
	_, _ = fs.ReadFile(wrapper{fixtures}, "fixture.txt")
	_, _ = fs.ReadFile(wrapper{os.DirFS("testdata")}, "fixture.txt") // want "TFS001: access io/fs.ReadFile"
	_, _ = fs.ReadFile(diskWrapper{fixtures}, "fixture.txt")         // want "TFS001: access io/fs.ReadFile"
	w := wrapper{fixtures}
	w.FS = os.DirFS("mutated")
	_, _ = fs.ReadFile(w, "file") // want "TFS001: access io/fs.ReadFile"
}

func TestClosures(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db")
	f := func() { read(p) }
	f()
	t.Run("safe", func(t *testing.T) { read(p) })
	q := "fixture"
	g := func() { read(q) }
	g() // want "TFS001: read os.ReadFile"
}

func tempParent(tb testing.TB) string { return tb.TempDir() }
func TestTemporaryInterfaces(t *testing.T) {
	read(filepath.Join(tempParent(t), "db"))
	p, _ := os.MkdirTemp(tempParent(t), "nested-*")
	read(filepath.Join(p, "db"))
	f, _ := os.CreateTemp("", "test-*")
	read(f.Name())
	root, _ := os.OpenRoot(t.TempDir())
	_, _ = root.Open("db")
	_, _ = fs.ReadFile(root.FS(), "db")
}

func TestSymlink(t *testing.T) {
	p := filepath.Join(t.TempDir(), "link")
	_ = os.Symlink("/outside", p) // want "TFS003: write os.Symlink"
	read(p)                       // want "TFS002: read os.ReadFile"
}

func recursive(path string, n int) {
	if n == 0 {
		read(path)
		return
	}
	recursive(path, n-1)
}
func TestRecursion(t *testing.T) {
	recursive("fixture", 2) // want "TFS001: read os.ReadFile"
	recursive(filepath.Join(t.TempDir(), "db"), 2)
}

func notATest(t *testing.T)        { read("unreachable") }
func BenchmarkFuture(b *testing.B) { read("benchmark-out-of-scope") }
