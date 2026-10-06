package audit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseLog(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	log := ParseLog([]byte("# test log\ngetenv SECRET\nopen fixture with spaces\nchdir "+sub+"\nstat ../after\nopen missing\n"), root, root, filepath.Join(root, "separate-temp"))
	if log.Status != "complete" || len(log.Records) != 6 {
		t.Fatalf("%+v", log)
	}
	if r := log.Records[0]; r.Environment != "GODEBUG" || !r.Implicit || r.Sequence != 0 {
		t.Fatalf("implicit runtime input = %+v", r)
	}
	if r := log.Records[1]; r.Environment != "SECRET" || r.Implicit || r.Sequence != 1 {
		t.Fatalf("logged environment = %+v", r)
	}
	if r := log.Records[2]; r.RawPath != "fixture with spaces" || r.Sequence != 2 || r.Path != filepath.Join(root, "fixture with spaces") || r.Class != "checkout/module" || !r.CacheRelevant {
		t.Fatalf("%+v", r)
	}
	if log.Records[4].Path != filepath.Join(root, "after") {
		t.Fatalf("chdir not applied: %+v", log)
	}
}

func TestSymlinkCacheAndDisplay(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "checkout")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	log := ParseLog([]byte("# test log\nopen "+link+"\n"), root, root, "")
	if r := log.Records[1]; !r.CacheRelevant || r.Class != "external" || !r.Ambiguous {
		t.Fatalf("cache filtering must remain distinct from realpath display: %+v", r)
	}
}

func TestBadLogs(t *testing.T) {
	for _, tt := range []struct{ data, status string }{
		{"", "malformed"}, {"# other log\n", "malformed"}, {"# test log\nopen truncated", "partial"}, {"# test log\nother name\n", "malformed"}, {"# test log\nopen \n", "malformed"}, {"# test log\n", "complete"},
	} {
		t.Run(tt.status+tt.data, func(t *testing.T) {
			if got := ParseLog([]byte(tt.data), t.TempDir(), "", ""); got.Status != tt.status {
				t.Fatalf("%+v", got)
			}
		})
	}
	if got := readLog(filepath.Join(t.TempDir(), "absent"), "", "", ""); got.Status != "missing" {
		t.Fatalf("%+v", got)
	}
}

func TestClassification(t *testing.T) {
	base := t.TempDir()
	checkout := filepath.Join(base, "checkout")
	temp := filepath.Join(base, "owned")
	log := ParseLog([]byte("# test log\nopen "+filepath.Join(checkout, "fixture")+"\nopen "+filepath.Join(temp, "data")+"\nopen "+filepath.Join(base, "other")+"\n"), checkout, checkout, temp)
	want := []string{"checkout/module", "dedicated-temporary", "external/cache-ignored"}
	for i, r := range log.Records[1:] {
		if r.Class != want[i] {
			t.Fatalf("%+v", r)
		}
	}
	if within(checkout, checkout+"-sibling") {
		t.Fatal("sibling prefix treated as contained")
	}
}

func TestArguments(t *testing.T) {
	got, err := packageArgs([]string{"-race", "-tags", "demo", "-run", "TestSomething", "./...", "-args", "user-value"})
	if err != nil || !reflect.DeepEqual(got, []string{"-race", "-tags", "demo", "./..."}) {
		t.Fatalf("%v %v", got, err)
	}
	for _, args := range [][]string{{"-exec", "wrapper"}, {"-exec=wrapper"}, {"-args", "-test.testlogfile=x"}, {"-count=2"}, {"-args", "-test.count=2"}, {"-c"}, {"-list=."}, {"-fuzz=Fuzz"}, {"-n"}, {"-run"}} {
		if _, err := packageArgs(args); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
}

func TestVersionGate(t *testing.T) {
	for _, v := range []string{"go1.26.9", "go1.28.0", "devel go1.27", "go1.27rc1", "go1.27.0-custom"} {
		if supportedVersion(v, "linux") {
			t.Errorf("accepted %s", v)
		}
	}
	for _, p := range []string{"linux", "darwin", "windows"} {
		if !supportedVersion("go1.27.0", p) {
			t.Errorf("rejected %s", p)
		}
	}
}

func TestQuoteExec(t *testing.T) {
	for _, s := range []string{"/a path/testfs", `C:\a path\testfs.exe`, `/quote's/testfs`, `/double"quote/testfs`} {
		q, err := quoteExec(s)
		if err != nil {
			t.Fatal(err)
		}
		parts, err := splitGoFlags(q)
		if err != nil || len(parts) != 1 || parts[0] != s {
			t.Fatalf("%q %v %v", q, parts, err)
		}
	}
	if _, err := quoteExec(`a'"b`); err == nil {
		t.Fatal("unrepresentable path accepted")
	}
	if _, err := splitGoFlags(`"unfinished`); err == nil {
		t.Fatal("unfinished quote accepted")
	}
	if strings.Contains(strings.Join(CoverageLimits, " "), "read confirmed") {
		t.Fatal("misleading terminology")
	}
}

func TestEnvironmentFindingsPreserveNamesAndDistinguishImplicitGODEBUG(t *testing.T) {
	log := ParseLog([]byte("# test log\ngetenv GODEBUG\ngetenv name with spaces\n"), t.TempDir(), "", "")
	findings, _ := findingsFromRecords("p", log.Records, func(Record) bool { return true })
	if len(findings) != 3 || findings[0].Environment != "GODEBUG" || findings[0].Evidence != "implicit" || findings[0].Confidence != "implicit" || findings[1].Environment != "GODEBUG" || findings[1].Evidence != "observed" || findings[2].Environment != "name with spaces" {
		t.Fatalf("environment findings = %+v", findings)
	}
	for _, f := range findings {
		if f.Path != "" || f.Class != "" || strings.Contains(f.Reason, "value") && strings.Contains(f.Reason, "=") {
			t.Fatalf("environment leaked filesystem/value data: %+v", f)
		}
	}
	partial := ParseLog([]byte("# test log\ngetenv NAME"), t.TempDir(), "", "")
	if len(partial.Records) != 0 {
		t.Fatalf("incomplete final record retained or implicit GODEBUG added: %+v", partial)
	}
}

func TestAggregateMergesRepeatedPackageInvocations(t *testing.T) {
	invocations := []Invocation{
		{Package: "example.test/p", Log: Log{Records: []Record{{Operation: "open", Path: "/fixture", Class: "checkout/module", CacheRelevant: true}, {Operation: "getenv", Environment: "FIXTURE", CacheRelevant: true}}}},
		{Package: "example.test/p", Log: Log{Records: []Record{{Operation: "open", Path: "/fixture", Class: "checkout/module", CacheRelevant: true}, {Operation: "getenv", Environment: "FIXTURE", CacheRelevant: true}}}},
	}
	findings := aggregate(invocations)
	if len(findings) != 2 || findings[0].Environment != "FIXTURE" || findings[0].Count != 2 || findings[1].Path != "/fixture" || findings[1].Count != 2 {
		t.Fatalf("aggregate = %+v, want repeated filesystem and environment findings", findings)
	}
}
