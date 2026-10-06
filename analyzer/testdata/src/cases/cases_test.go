package cases

import (
	"embed"
	"io/fs"
	stdos "os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"text/template"
)

//go:embed fixture.txt
var fixtures embed.FS

var _, initErr = stdos.ReadFile("init-fixture") // want "TFS001: read os.ReadFile"

func init() { _, _ = stdos.Stat("init-stat") } // want "TFS001: metadata os.Stat"

func TestDirect(t *testing.T) {
	_, _ = stdos.ReadFile("testdata/a") // want "TFS001: read os.ReadFile"
	_, _ = stdos.Stat("testdata")       // want "TFS001: metadata os.Stat"
	_, _ = stdos.Lstat("testdata")      // want "TFS001: metadata os.Lstat"
	_, _ = stdos.ReadDir(".")           // want "TFS001: directory os.ReadDir"
	_, _ = filepath.Glob("*.go")        // want "TFS001: directory path/filepath.Glob"
	_ = filepath.WalkDir(".", nil)      // want "TFS001: directory path/filepath.WalkDir"
	unrelated{}.ReadFile("testdata")
	_, _ = stdos.Open(unrelated{}.TempDir()) // want "TFS001: open os.Open"
}

func TestEmbedded(t *testing.T) {
	_, _ = fixtures.ReadFile("fixture.txt")
	var f fs.FS = fixtures
	_, _ = fs.ReadFile(f, "fixture.txt")
	readFS(f)
	sub, _ := fs.Sub(f, "sub")
	readFS(sub)
	_, _ = template.ParseFS(f, "*.txt")
	readFS(fstest.MapFS{"fixture.txt": {Data: []byte("hello")}})
	data, _ := stdos.ReadFile("fixture.txt") // want "TFS001: read os.ReadFile"
	readFS(fstest.MapFS{"fixture.txt": {Data: data}})
}

func TestTemporary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db")
	_, _ = stdos.Open(path)
	_, _ = stdos.Stat(path)
	data, _ := fixtures.ReadFile("fixture.txt")
	_ = stdos.WriteFile(path, data, 0600)
	read(path)
	read(joined(dir))
	read(dir + "/db")
	dir2, _ := stdos.MkdirTemp("", "testfs-*")
	read(filepath.Join(dir2, "db"))
	f, _ := stdos.CreateTemp(dir, "testfs-*")
	read(f.Name())
	read(filepath.Join(stdos.TempDir(), "pre-existing")) // want "TFS001: read os.ReadFile"
	_, _ = stdos.MkdirTemp("testdata", "new-*")          // want "TFS003: write os.MkdirTemp"
}

func TestMixed(t *testing.T) {
	p := t.TempDir()
	if stdos.Getenv("BRANCH") != "" {
		p = "fixture.txt"
	}
	read(filepath.Join(p, "db"))                           // want "TFS001: read os.ReadFile"
	read(filepath.Join(t.TempDir(), "..", "escape"))       // want "TFS002: read os.ReadFile"
	read(filepath.Join(t.TempDir(), stdos.Getenv("NAME"))) // want "TFS002: read os.ReadFile"
	read(t.TempDir() + "suffix")                           // want "TFS002: read os.ReadFile"
}

func TestDiskFS(t *testing.T) {
	f := stdos.DirFS("testdata")
	_ = stdos.DirFS("unused")
	sub, _ := fs.Sub(f, "sub")
	readFS(sub)            // want "TFS001: access io/fs.ReadFile"
	_, _ = fs.Glob(f, "*") // want "TFS001: access io/fs.Glob"
	_, _ = f.Open("x")     // want "TFS001: access io/fs.Open"
}

func TestWrites(t *testing.T) {
	_ = stdos.WriteFile("generated", nil, 0600)                             // want "TFS003: write os.WriteFile"
	_, _ = stdos.OpenFile("fixture", stdos.O_RDONLY, 0)                     // want "TFS001: open os.OpenFile"
	_, _ = stdos.OpenFile("generated", stdos.O_WRONLY|stdos.O_CREATE, 0600) // want "TFS003: write/open os.OpenFile"
	_, _ = template.ParseFiles("a", "b")                                    // want "TFS001: read text/template.ParseFiles" "TFS001: read text/template.ParseFiles"
	_, _ = template.New("t").ParseGlob("*.go")                              // want "TFS001: directory text/template.ParseGlob"
}

func TestHelpers(t *testing.T) {
	read("fixture") // want "TFS001: read os.ReadFile"
	//nolint:testfs // Intentional source contract, interpreted by the host only.
	read("source.go") // want "TFS001: read os.ReadFile"
	f := func() { read("closure") }
	f()                                                  // want "TFS001: read os.ReadFile"
	t.Run("sub", func(t *testing.T) { read("subtest") }) // want "TFS001: read os.ReadFile"
	t.Cleanup(func() { read("cleanup") })                // want "TFS001: read os.ReadFile"
}

func TestUnknown(t *testing.T) {
	var f fs.FS
	readFS(f) // want "TFS002: access io/fs.ReadFile"
}

func TestMain(m *testing.M) {
	_, _ = stdos.Stat("setup") // want "TFS001: metadata os.Stat"
}

func Example_contract() {
	read("example") // want "TFS001: read os.ReadFile"
}

func FuzzSeed(f *testing.F) {
	f.Fuzz(func(t *testing.T, s string) { read("seed") }) // want "TFS001: read os.ReadFile"
}
