// Package analyzer identifies potential runtime filesystem dependencies in Go tests.
package analyzer

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"

	"github.com/lukasschwab/testinputs/report"
)

const SchemaVersion = report.SchemaVersion

// Analyzer is the standard go/analysis entry point. New creates an independent
// instance, useful for hosts that configure flags or run analyses concurrently.
var Analyzer = New()

func New() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name:       "testinputs",
		Doc:        "report potential runtime filesystem dependencies in tests",
		FactTypes:  []analysis.Fact{new(functionFact)},
		ResultType: reflect.TypeOf((*Result)(nil)),
	}
	uncertain := a.Flags.Bool("uncertain", true, "report TFS002 findings with unresolved provenance")
	reportDir := a.Flags.String("report-dir", "", "write versioned JSON per test package to this directory")
	a.Run = func(pass *analysis.Pass) (any, error) {
		r, err := run(pass, *uncertain)
		if err != nil {
			return r, err
		}
		if *reportDir != "" {
			for _, f := range pass.Files {
				if strings.HasSuffix(pass.Fset.Position(f.Pos()).Filename, "_test.go") {
					if err = os.MkdirAll(*reportDir, 0755); err != nil {
						return nil, err
					}
					data, err := json.MarshalIndent(r, "", "  ")
					if err != nil {
						return nil, err
					}
					name := fmt.Sprintf("%x.json", sha256.Sum256([]byte(pass.Pkg.Path())))
					if err = os.WriteFile(filepath.Join(*reportDir, name), append(data, '\n'), 0600); err != nil {
						return nil, err
					}
					break
				}
			}
		}
		return r, nil
	}
	return a
}

type Location = report.Location
type Finding = report.Finding

// Result is available to analysis hosts through Pass.ResultOf. The standard
// singlechecker -json flag uses the go/analysis diagnostic envelope instead.
type Result struct {
	Package         string    `json:"package"`
	SchemaVersion   int       `json:"schema_version"`
	Findings        []Finding `json:"findings"`
	Limitations     []string  `json:"limitations"`
	UnresolvedCalls int       `json:"unresolved_calls"`
}

type engine struct {
	pass      *analysis.Pass
	pkg       *ssa.Package
	functions []*ssa.Function
	summaries map[*ssa.Function]summary
	globals   map[*ssa.Global][]ssa.Value
}

func run(pass *analysis.Pass, uncertain bool) (*Result, error) {
	r := &Result{Package: pass.Pkg.Path(), SchemaVersion: SchemaVersion, Findings: []Finding{}, Limitations: []string{
		"Static findings are potential dependencies, not observed access or reproduced cache misses.",
		"Reflection, unsafe, unresolved dynamic dispatch, subprocess I/O, and arbitrary heap aliasing are not resolved.",
		"Temporary paths are an intended testing pattern; symlinks and TMPDIR inside a module can still affect caching.",
	}}
	if pass.Pkg.Name() == "main" && strings.HasSuffix(pass.Pkg.Path(), ".test") {
		return r, nil
	}
	// Model standard library operations explicitly instead of summarizing their
	// implementations, which contain platform internals outside this contract.
	if len(pass.Files) == 0 || strings.HasPrefix(pass.Fset.Position(pass.Files[0].Pos()).Filename, filepath.Join(runtime.GOROOT(), "src")+string(filepath.Separator)) {
		return r, nil
	}
	prog := ssa.NewProgram(pass.Fset, ssa.InstantiateGenerics)
	var imports func(*types.Package)
	imports = func(p *types.Package) {
		if prog.Package(p) != nil {
			return
		}
		for _, imp := range p.Imports() {
			imports(imp)
		}
		prog.CreatePackage(p, nil, nil, true)
	}
	for _, p := range pass.Pkg.Imports() {
		imports(p)
	}
	pkg := prog.CreatePackage(pass.Pkg, pass.Files, pass.TypesInfo, false)
	pkg.Build()
	e := &engine{pass: pass, pkg: pkg, summaries: map[*ssa.Function]summary{}, globals: map[*ssa.Global][]ssa.Value{}}
	seen := map[*ssa.Function]bool{}
	var add func(*ssa.Function)
	add = func(fn *ssa.Function) {
		if fn == nil || seen[fn] || fn.Pkg != pkg {
			return
		}
		seen[fn] = true
		e.functions = append(e.functions, fn)
		for _, child := range fn.AnonFuncs {
			add(child)
		}
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				if call, ok := ins.(ssa.CallInstruction); ok {
					add(call.Common().StaticCallee())
				}
				if st, ok := ins.(*ssa.Store); ok {
					if g, ok := st.Addr.(*ssa.Global); ok {
						e.globals[g] = append(e.globals[g], st.Val)
					}
				}
			}
		}
	}
	add(pkg.Func("init"))
	// AST order makes summaries and diagnostics reproducible.
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok {
				if obj, ok := pass.TypesInfo.Defs[fd.Name].(*types.Func); ok {
					add(prog.FuncValue(obj))
				}
			}
		}
	}
	for _, fn := range e.functions {
		e.summaries[fn] = summary{}
	}
	stable := false
	for iteration := 0; iteration < 12; iteration++ {
		changed := false
		for _, fn := range e.functions {
			s := e.summarize(fn)
			if !reflect.DeepEqual(s, e.summaries[fn]) {
				changed = true
				e.summaries[fn] = s
			}
		}
		if !changed {
			stable = true
			break
		}
	}
	if !stable {
		r.Limitations = append(r.Limitations, "Summary fixed-point budget exceeded; affected calls have incomplete coverage.")
	}
	for _, fn := range e.functions {
		if obj, ok := fn.Object().(*types.Func); ok && fn.Parent() == nil && obj.Pkg() == pass.Pkg && fn.Origin() == nil && obj.Name() != "init" && !strings.HasSuffix(e.location(fn.Pos()).File, "_test.go") {
			s := e.summaries[fn]
			s.Incomplete = s.Incomplete || !stable
			if len(s.Ops) > 0 || meaningful(s.Returns) || s.Incomplete || s.TaintsTemp {
				pass.ExportObjectFact(obj, &functionFact{Version: 1, Summary: s})
			}
		}
	}
	emitted := map[string]bool{}
	for _, fn := range e.functions {
		if !e.root(fn) {
			continue
		}
		s := e.summaries[fn]
		r.UnresolvedCalls += s.Unresolved
		for _, op := range s.Ops {
			if fn.Name() == "init" && !strings.HasSuffix(op.Site.File, "_test.go") {
				continue
			}
			origin := classify(op.Value)
			if s.TaintsTemp && origin&temporary != 0 {
				origin = (origin &^ temporary) | unknown
			}
			if origin != 0 && origin & ^(embedded|memory|temporary) == 0 {
				continue
			}
			rule, confidence, reason := "TFS002", "uncertain", "backing store or path safety could not be resolved"
			if origin&disk != 0 {
				rule, confidence, reason = "TFS001", "high", "runtime filesystem access may make cached test results depend on checkout metadata"
			}
			if op.Write {
				rule = "TFS003"
				reason = "disk write/open may add a runtime filesystem dependency"
			}
			if rule == "TFS002" && !uncertain {
				continue
			}
			key := fmt.Sprintf("%v/%s/%s/%s", op.Site, rule, op.Name, op.Value.key())
			if emitted[key] {
				continue
			}
			emitted[key] = true
			finding := Finding{Package: pass.Pkg.Path(), Rule: rule, Operation: op.Name, Evidence: "potential", Confidence: confidence, Reason: reason, Expression: op.Value.display(), Location: op.Site, Source: op.Source}
			r.Findings = append(r.Findings, finding)
			pos := e.position(op.Site)
			if !pos.IsValid() {
				continue
			}
			d := analysis.Diagnostic{Pos: pos, Category: rule, Message: fmt.Sprintf("%s: %s %s [confidence=%s]: %s; embed fixtures or use test-owned temporary paths", rule, op.Name, op.Value.display(), confidence, reason)}
			if src := e.position(op.Source); src.IsValid() && src != pos {
				d.Related = []analysis.RelatedInformation{{Pos: src, Message: "filesystem operation originates here"}}
			}
			pass.Report(d)
		}
		if s.Incomplete || !stable {
			r.Limitations = append(r.Limitations, "Bounded analysis was incomplete for "+fn.Name())
			if uncertain && fn.Pos().IsValid() {
				pass.Reportf(fn.Pos(), "TFS002: analysis [confidence=uncertain]: summary budget exceeded; filesystem coverage is incomplete")
			}
		}
	}
	return r, nil
}

func (e *engine) location(pos token.Pos) Location {
	p := e.pass.Fset.Position(pos)
	return Location{File: p.Filename, Line: p.Line, Column: p.Column}
}
func (e *engine) position(loc Location) token.Pos {
	for _, f := range e.pass.Files {
		tf := e.pass.Fset.File(f.Pos())
		if tf.Name() == loc.File && loc.Line > 0 && loc.Line <= tf.LineCount() {
			return tf.LineStart(loc.Line) + token.Pos(loc.Column-1)
		}
	}
	return token.NoPos
}

func (e *engine) root(fn *ssa.Function) bool {
	if fn.Name() == "init" && fn.Synthetic != "" {
		return true
	}
	if fn.Parent() != nil || !strings.HasSuffix(e.location(fn.Pos()).File, "_test.go") || fn.Signature.Recv() != nil {
		return false
	}
	if strings.HasPrefix(fn.Name(), "init#") {
		return true
	}
	sig := fn.Signature
	if sig.Results().Len() != 0 {
		return false
	}
	if testName(fn.Name(), "Example") {
		return sig.Params().Len() == 0
	}
	if sig.Params().Len() != 1 {
		return false
	}
	param := sig.Params().At(0).Type()
	if fn.Name() == "TestMain" {
		return named(param, "testing", "M")
	}
	return testName(fn.Name(), "Test") && named(param, "testing", "T") || testName(fn.Name(), "Fuzz") && named(param, "testing", "F")
}

func testName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := strings.TrimPrefix(name, prefix)
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return !unicode.IsLower(r)
}

func named(t types.Type, pkg, name string) bool {
	t = types.Unalias(t)
	if ptr, ok := t.(*types.Pointer); ok {
		t = types.Unalias(ptr.Elem())
	}
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == pkg && n.Obj().Name() == name
}
