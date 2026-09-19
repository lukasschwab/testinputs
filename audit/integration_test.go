package audit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "testfs binary with spaces-*")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "testfs")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, "../cmd/testfs")
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		panic(string(out) + err.Error())
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func module(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "checkout with spaces")
	if err := os.CopyFS(dir, os.DirFS("testdata/module")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func invoke(t *testing.T, dir string, opts, goArgs []string) (Report, int, string) {
	t.Helper()
	reportFile := filepath.Join(t.TempDir(), "report.json")
	args := append([]string{"audit", "-json", reportFile}, opts...)
	args = append(args, "--")
	args = append(args, goArgs...)
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(reportFile)
	if err != nil {
		t.Fatalf("report: %v\n%s", err, out)
	}
	var r Report
	if err = json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r, code, string(out)
}

func TestAuditIntegration(t *testing.T) {
	dir := module(t)
	r, code, out := invoke(t, dir, []string{"-temp-base", t.TempDir()}, []string{"-race", "./observe", "./second"})
	if code != 0 || len(r.Errors) > 0 || len(r.Invocations) != 2 {
		t.Fatalf("code=%d report=%+v\n%s", code, r, out)
	}
	if !r.TempEnvironmentChanged || r.TempBase == "" {
		t.Fatal("temporary environment not recorded")
	}
	seen := map[string]Finding{}
	for _, f := range r.Findings {
		seen[filepath.Base(f.Path)] = f
		if f.Evidence != "observed" || f.Operation == "read" {
			t.Fatalf("%+v", f)
		}
	}
	for _, name := range []string{"fixture space.txt", "library fixture.txt", "missing file", "write only", "second.txt", "after.txt", "parallel.txt"} {
		if _, ok := seen[name]; !ok {
			t.Errorf("missing observation %s\n%s", name, out)
		}
	}
	if _, ok := seen["embedded.txt"]; ok {
		t.Error("embedded content recorded as runtime disk access")
	}
	if seen["fixture space.txt"].Class != "checkout/module" {
		t.Error("checkout under system temp was hidden")
	}
	if seen["owned.txt"].Class != "dedicated-temporary" {
		t.Errorf("temp classification: %+v", seen["owned.txt"])
	}
	for _, inv := range r.Invocations {
		if inv.Log.Status != "complete" || inv.Package == "" || !inv.Finished {
			t.Fatalf("%+v", inv)
		}
	}
}

func TestAuditOutcomes(t *testing.T) {
	dir := module(t)
	for _, tt := range []struct {
		name, pkg      string
		opts, flags    []string
		code           int
		collectorError bool
	}{
		{"failure", "./failure", nil, nil, 1, false},
		{"timeout", "./timeout", nil, []string{"-timeout=50ms"}, 1, true},
		{"missing log", "./nolog", nil, nil, 2, true},
		{"partial log", "./partial", nil, nil, 2, true},
		{"policy", "./second", []string{"-fail-on-checkout"}, nil, 3, false},
		{"arguments", "./second", nil, []string{"-run=TestSecond", "-args", "--", "a value"}, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{tt.pkg}, tt.flags...)
			r, code, out := invoke(t, dir, tt.opts, args)
			if code != tt.code || (len(r.Errors) > 0) != tt.collectorError {
				t.Fatalf("code=%d errors=%v\n%s", code, r.Errors, out)
			}
		})
	}
}

func TestAuditBlindSpots(t *testing.T) {
	r, code, out := invoke(t, module(t), nil, []string{"./blind"})
	if code != 0 {
		t.Fatalf("%d\n%s", code, out)
	}
	for _, f := range r.Findings {
		if strings.Contains(filepath.Base(f.Path), "unlogged") {
			t.Fatalf("unexpected observation %+v", f)
		}
	}
	if len(r.Limitations) < 7 {
		t.Fatal("missing coverage limitations")
	}
}

func TestVettoolIntegration(t *testing.T) {
	dir := module(t)
	cmd := exec.Command("go", "vet", "-vettool="+binary, "./static")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	if err == nil || strings.Count(string(out), "TFS001:") != 2 {
		t.Fatalf("expected two standard vettool diagnostics (direct and imported): %v\n%s", err, out)
	}
}
