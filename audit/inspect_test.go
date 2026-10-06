package audit

import (
	"bytes"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed all:testdata/inspection/linux
var inspectionFixtures embed.FS

// Translate captured path suffixes without changing relative operation names.
func inspectionPathReplacer(root, external, temp string) *strings.Replacer {
	sep := string(filepath.Separator)
	return strings.NewReplacer(
		"@ROOT@/", root+sep, "@ROOT@", root,
		"@EXTERNAL@/", external+sep, "@EXTERNAL@", external,
		"@TEMP@/", temp+sep, "@TEMP@", temp,
	)
}

func rebindInspectionMetadata(data []byte, root string) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for {
		var pkg inspectionPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			return output.Bytes(), nil
		} else if err != nil {
			return nil, err
		}
		pkg.Dir = inspectionPathReplacer(root, "", "").Replace(pkg.Dir)
		pkg.Root = inspectionPathReplacer(root, "", "").Replace(pkg.Root)
		if err := encoder.Encode(pkg); err != nil {
			return nil, err
		}
	}
}

func TestInspectionMetadataRebindingEscapesPaths(t *testing.T) {
	data, err := inspectionFixtures.ReadFile("testdata/inspection/linux/packages.json")
	if err != nil {
		t.Fatal(err)
	}
	root := `C:\Users\runner\checkout "quoted"`
	data, err = rebindInspectionMetadata(data, root)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for _, want := range []string{root, root + string(filepath.Separator) + "second"} {
		var pkg inspectionPackage
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatal(err)
		}
		if pkg.Dir != want || pkg.Root != root {
			t.Fatalf("metadata paths = %q, %q; want %q, %q", pkg.Dir, pkg.Root, want, root)
		}
	}
}

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
		if e.Name() == "packages.json" {
			b, err = rebindInspectionMetadata(b, root)
			if err != nil {
				return err
			}
		} else {
			b = []byte(inspectionPathReplacer(root, external, temp).Replace(string(b)))
		}
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
			want[inspectionPathReplacer(filepath.Join(base, "checkout with spaces"), filepath.Join(base, "external"), filepath.Join(base, "temporary")).Replace(line)] = true
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

func TestInspectionCLIAndNonMutation(t *testing.T) {
	work, meta := materializeInspection(t)
	before, err := os.ReadFile(filepath.Join(work, "b001", "testlog.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if code := inspectMain(config{Work: work, PackagesJSON: meta}, nil); code != 1 {
		t.Fatalf("successful inspection code=%d", code)
	}
	if code := inspectMain(config{Work: work, PackagesJSON: meta}, []string{"./..."}); code != ExitAuditFailure {
		t.Fatalf("package args code=%d", code)
	}
	if code := inspectMain(config{Work: work, PackagesJSON: meta, KeepLogs: true}, nil); code != ExitAuditFailure {
		t.Fatalf("collection flag code=%d", code)
	}
	after, err := os.ReadFile(filepath.Join(work, "b001", "testlog.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("inspection modified preserved log")
	}
}

func TestInspectionIdentityAndMetadataErrors(t *testing.T) {
	work, meta := materializeInspection(t)
	if err := os.Remove(filepath.Join(work, "b001", "_testmain.go")); err != nil {
		t.Fatal(err)
	}
	r := inspectWork(config{Work: work, PackagesJSON: meta})
	if len(r.Packages[0].Errors) == 0 {
		t.Fatalf("missing identity accepted: %+v", r)
	}
	for _, data := range []string{"not json", `{"ImportPath":"a","Dir":"relative"}`, `{"ImportPath":"a","Error":{"Err":"unavailable"}}`} {
		if err := os.WriteFile(meta, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		r = inspectWork(config{Work: work, PackagesJSON: meta})
		if len(r.Errors) == 0 {
			t.Fatalf("metadata accepted: %s", data)
		}
	}
}

func TestInspectionLogFilteringContracts(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	log := ParseLog([]byte("# test log\nopen temporary\nchdir "+outside+"\nopen child\n"), root, "", "")
	findings, ignored := findingsFromRecords("p", log.Records, func(r Record) bool { return r.CacheRelevant })
	if len(findings) != 1 || findings[0].Operation != "chdir" || ignored != 2 {
		t.Fatalf("%+v ignored=%d", findings, ignored)
	}
	if cacheContains(root, root+string(filepath.Separator)+".."+string(filepath.Separator)+"elsewhere") != true {
		t.Fatal("lexical absolute containment changed")
	}
	if cacheContains(root, root+"-sibling") {
		t.Fatal("sibling prefix included")
	}
}

func TestInspectionAggregationAndNoRoot(t *testing.T) {
	root := t.TempDir()
	log := ParseLog([]byte("# test log\nopen a b\nopen ./a b\nopen a b\n"), root, root, "")
	findings, ignored := findingsFromRecords("p", log.Records, func(r Record) bool { return r.CacheRelevant })
	if ignored != 0 || len(findings) != 1 || findings[0].Count != 3 {
		t.Fatalf("%+v ignored=%d", findings, ignored)
	}
	noRoot := ParseLog([]byte("# test log\nopen temporary\nchdir "+root+"\n"), root, "", "")
	findings, ignored = findingsFromRecords("p", noRoot.Records, func(r Record) bool { return r.CacheRelevant })
	if len(findings) != 1 || findings[0].Operation != "chdir" || ignored != 1 {
		t.Fatalf("%+v ignored=%d", findings, ignored)
	}
}

func TestInspectionTemporaryUnderRootIsSelected(t *testing.T) {
	root := t.TempDir()
	temp := filepath.Join(root, "tmp", "owned")
	log := ParseLog([]byte("# test log\nopen "+temp+"\n"), root, root, "")
	findings, ignored := findingsFromRecords("p", log.Records, func(r Record) bool { return r.CacheRelevant })
	if len(findings) != 1 || ignored != 0 {
		t.Fatalf("%+v ignored=%d", findings, ignored)
	}
}

func TestInspectionCLIJSONAndCleanAndEmpty(t *testing.T) {
	work, meta := materializeInspection(t)
	if err := os.WriteFile(filepath.Join(work, "b001", "testlog.txt"), []byte("# test log\n"), 0600); err != nil {
		t.Fatal(err)
	}
	jsonFile := filepath.Join(t.TempDir(), "inspection.json")
	if code := inspectMain(config{Work: work, PackagesJSON: meta, JSON: jsonFile}, nil); code != 0 {
		t.Fatalf("clean JSON code=%d", code)
	}
	if data, err := os.ReadFile(jsonFile); err != nil || !strings.Contains(string(data), `"packages"`) {
		t.Fatalf("JSON report: %v %s", err, data)
	}
	empty := t.TempDir()
	if code := inspectMain(config{Work: empty, PackagesJSON: meta}, nil); code != ExitAuditFailure {
		t.Fatalf("empty work code=%d", code)
	}
}
