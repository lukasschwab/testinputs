package audit

import (
	"embed"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed testdata/inspection/packages.json testdata/inspection/work/b001/_testmain.go testdata/inspection/work/b001/testlog.txt testdata/inspection/work/b002/_testmain.go testdata/inspection/work/b002/testlog.txt testdata/inspection/expected-hash-inputs.txt
var inspectionFixtures embed.FS

func materializeInspection(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "checkout")
	for _, name := range []string{"work/b001/_testmain.go", "work/b001/testlog.txt", "work/b002/_testmain.go", "work/b002/testlog.txt", "packages.json", "expected-hash-inputs.txt"} {
		b, err := inspectionFixtures.ReadFile("testdata/inspection/" + name)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		s := strings.ReplaceAll(string(b), "@ROOT@", root)
		s = strings.ReplaceAll(s, "/OUTSIDE", filepath.Join(base, "outside"))
		if err := os.WriteFile(target, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(base, "work"), filepath.Join(base, "packages.json")
}
func TestInspectWorkEmbeddedFixture(t *testing.T) {
	work, meta := materializeInspection(t)
	r := inspectWork(config{Work: work, PackagesJSON: meta})
	if len(r.Errors) != 0 || len(r.Packages) != 2 {
		t.Fatalf("%+v", r)
	}
	a := r.Packages[0]
	if len(a.Findings) != 3 || a.Ignored != 2 {
		t.Fatalf("%+v", a)
	}
	wantData, err := inspectionFixtures.ReadFile("testdata/inspection/expected-hash-inputs.txt")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(work)
	want := strings.Split(strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(wantData), "@ROOT@", filepath.Join(base, "checkout")), "@OUTSIDE@", filepath.Join(base, "outside"))), "\n")
	got := make([]string, len(a.Findings))
	for i, f := range a.Findings {
		got[i] = f.Operation + " " + f.Path
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("records = %q, want independent Go hash-input capture %q", got, want)
	}
}
func TestInspectRejectsRelativeChdir(t *testing.T) {
	work, meta := materializeInspection(t)
	if err := os.WriteFile(filepath.Join(work, "b001", "testlog.txt"), []byte("# test log\nchdir relative\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r := inspectWork(config{Work: work, PackagesJSON: meta})
	if len(r.Packages[0].Errors) == 0 {
		t.Fatalf("%+v", r)
	}
}
