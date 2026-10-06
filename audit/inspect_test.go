package audit

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed all:testdata/inspection/linux
var inspectionFixtures embed.FS

func materializeInspection(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	work, root := filepath.Join(base, "work"), filepath.Join(base, "checkout with spaces")
	external, temp := filepath.Join(base, "external"), filepath.Join(base, "temporary")
	for _, d := range []string{work, root, filepath.Join(root, "second"), external, temp} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	err := fs.WalkDir(inspectionFixtures, "testdata/inspection/linux", func(name string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		b, err := inspectionFixtures.ReadFile(name)
		if err != nil {
			return err
		}
		b = []byte(strings.NewReplacer("@ROOT@", root, "@EXTERNAL@", external, "@TEMP@", temp).Replace(string(b)))
		target := filepath.Join(work, strings.TrimPrefix(name, "testdata/inspection/linux/"))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return work, filepath.Join(work, "packages.json")
}
func TestInspectWorkEmbeddedFixture(t *testing.T) {
	work, meta := materializeInspection(t)
	r := inspectWork(config{Work: work, PackagesJSON: meta})
	if len(r.Errors) != 0 || len(r.Packages) != 2 {
		t.Fatalf("%+v", r)
	}
	got := map[string]bool{}
	ignored := 0
	for _, p := range r.Packages {
		if len(p.Errors) > 0 {
			t.Fatalf("%+v", p)
		}
		ignored += p.Ignored
		for _, f := range p.Findings {
			got[f.Operation+" "+f.Path] = true
		}
	}
	if ignored < 3 {
		t.Fatalf("external/temporary records not ignored: %+v", r.Packages)
	}
	b, err := inspectionFixtures.ReadFile("testdata/inspection/linux/expected-inputs.txt")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(work)
	want := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			want[strings.NewReplacer("@ROOT@", filepath.Join(base, "checkout with spaces"), "@EXTERNAL@", filepath.Join(base, "external"), "@TEMP@", filepath.Join(base, "temporary")).Replace(line)] = true
		}
	}
	if !sameSet(got, want) {
		t.Fatalf("records=%v want independent Go hash-input capture=%v", got, want)
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
func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func TestInspectionCoverageErrorsTakePrecedence(t *testing.T) {
	work, meta := materializeInspection(t)
	// b001 has captured findings; b002 is intentionally incomplete.
	if err := os.Remove(filepath.Join(work, "b002", "testlog.txt")); err != nil {
		t.Fatal(err)
	}
	r := inspectWork(config{Work: work, PackagesJSON: meta})
	if len(r.Packages[0].Findings) == 0 || len(r.Packages[1].Errors) == 0 {
		t.Fatalf("expected finding and later coverage error: %+v", r)
	}
	if got := inspectionExitCode(r); got != ExitAuditFailure {
		t.Fatalf("exit code=%d, want coverage failure", got)
	}
}

func TestInspectionGlobalMetadataErrorTakesPrecedence(t *testing.T) {
	work, meta := materializeInspection(t)
	if err := os.WriteFile(meta, []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	r := inspectWork(config{Work: work, PackagesJSON: meta})
	if len(r.Errors) == 0 || len(r.Packages[0].Findings) != 0 {
		t.Fatalf("expected global metadata error: %+v", r)
	}
	// Model the report that can contain earlier observations plus a later
	// metadata failure; exit policy must still prioritize coverage.
	r.Packages[0].Findings = []Finding{{Package: "example", Count: 1}}
	if got := inspectionExitCode(r); got != ExitAuditFailure {
		t.Fatalf("exit code=%d, want coverage failure", got)
	}
}
