package analyzer

import (
	"encoding/json"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/packages"
)

func TestAnalyzer(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), New(), "cases", "external")
	for _, result := range results {
		if result.Action.Err != nil {
			continue
		}
		r, ok := result.Action.Result.(*Result)
		if !ok {
			t.Fatal("missing typed result")
		}
		if r.SchemaVersion != SchemaVersion {
			t.Fatal("missing schema version")
		}
		for _, f := range r.Findings {
			if f.Rule == "TFS001" && f.Confidence != "high" {
				t.Fatalf("%+v", f)
			}
		}
	}
}

func TestFilteringAndJSON(t *testing.T) {
	a := New()
	if err := a.Flags.Set("uncertain", "false"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := a.Flags.Set("report-dir", dir); err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), a, "filtered")
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("%v %v", files, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var r Result
	if err = json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.SchemaVersion != SchemaVersion || r.Package != "filtered" || len(r.Findings) != 1 || r.Findings[0].Rule != "TFS001" {
		t.Fatalf("%+v", r)
	}
}

// BenchmarkAnalyzer excludes package loading and measures the analysis itself.
// The generated modules exercise parameter substitution, facts, and temp joins.
func BenchmarkAnalyzer(b *testing.B) {
	for _, size := range []int{10, 1000} {
		b.Run(fmt.Sprintf("%d_helpers", size), func(b *testing.B) {
			dir := b.TempDir()
			write := func(name, data string) {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
					b.Fatal(err)
				}
			}
			write("go.mod", "module benchfixture\n\ngo 1.27.0\n")
			write("base.go", "package benchfixture\n")
			source := "package benchfixture\nimport (\"os\";\"path/filepath\";\"testing\")\n"
			for i := 0; i < size; i++ {
				source += fmt.Sprintf("func helper%d(p string) { _,_ = os.ReadFile(p) };func TestCase%d(t *testing.T) { helper%d(filepath.Join(t.TempDir(),\"db\")); helper%d(os.Getenv(\"FIXTURE\")) }\n", i, i, i, i)
			}
			write("cases_test.go", source)
			pkgs, err := packages.Load(&packages.Config{Dir: dir, Mode: packages.LoadAllSyntax, Tests: true, Env: append(os.Environ(), "GOWORK=off")}, ".")
			if err != nil {
				b.Fatal(err)
			}
			var p *packages.Package
			for _, candidate := range pkgs {
				if len(candidate.Syntax) == 2 {
					p = candidate
					break
				}
			}
			if p == nil {
				b.Fatal("test package not loaded")
			}
			pass := &analysis.Pass{Fset: p.Fset, Files: p.Syntax, Pkg: p.Types, TypesInfo: p.TypesInfo, Report: func(analysis.Diagnostic) {}, ImportObjectFact: func(types.Object, analysis.Fact) bool { return false }, ExportObjectFact: func(types.Object, analysis.Fact) {}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r, err := run(pass, true)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(r.UnresolvedCalls), "unresolved/op")
			}
		})
	}
}
