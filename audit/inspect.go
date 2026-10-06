package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testfs/report"
)

type inspectionPackage struct {
	ImportPath string
	Dir        string
	Root       string
	Error      *struct{ Err string }
}
type InspectionAction struct {
	Package  string    `json:"package"`
	Dir      string    `json:"dir"`
	Root     string    `json:"root"`
	Action   string    `json:"action"`
	Log      string    `json:"log"`
	Findings []Finding `json:"findings"`
	Ignored  int       `json:"ignored_filesystem_operations"`
	Errors   []string  `json:"errors"`
}
type InspectionReport struct {
	SchemaVersion int                `json:"schema_version"`
	FilterModel   string             `json:"filter_model"`
	Work          string             `json:"work"`
	Packages      []InspectionAction `json:"packages"`
	Errors        []string           `json:"errors"`
	Limitations   []string           `json:"limitations"`
}

func inspectMain(c config, args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "testfs audit: -work does not accept package arguments")
		return ExitAuditFailure
	}
	if c.JSON == "-" {
		fmt.Fprintln(os.Stderr, "testfs audit: -json requires a file; stdout is reserved for the inspection report")
		return ExitAuditFailure
	}
	r := inspectWork(c)
	if c.JSON != "" {
		b, err := json.MarshalIndent(r, "", "  ")
		if err == nil {
			err = os.WriteFile(c.JSON, append(b, '\n'), 0600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "testfs audit:", err)
			return ExitAuditFailure
		}
	} else {
		printInspection(os.Stdout, r)
	}
	for _, p := range r.Packages {
		if len(p.Errors) > 0 {
			return ExitAuditFailure
		}
		if len(p.Findings) > 0 {
			return 1
		}
	}
	if len(r.Errors) > 0 {
		return ExitAuditFailure
	}
	return 0
}

func inspectWork(c config) InspectionReport {
	r := InspectionReport{SchemaVersion: SchemaVersion, FilterModel: "go1.27", Packages: []InspectionAction{}, Errors: []string{}, Limitations: []string{
		"Reports observed package-level cache inputs, not individual test attribution or reproduced cache misses.", "Only preserved logs are inspected; cache hits or skipped packages may leave no logs or actions.", "Metadata is resolved now unless -packages-json is supplied and must describe the original checkout paths.", "Filtering models Go 1.27 computeTestInputsID/search.InDir; it does not compute hashes, check cache eligibility, or inspect environment inputs.", "Open records include failed and write-mode opens. Initialization, pre-m.Run setup, subprocesses, and direct syscalls may be unlogged.",
	}}
	work, err := filepath.Abs(c.Work)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return r
	}
	r.Work = work
	entries, err := os.ReadDir(work)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return r
	}
	seen := map[string]bool{}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		action := filepath.Join(work, e.Name())
		main, log := filepath.Join(action, "_testmain.go"), filepath.Join(action, "testlog.txt")
		_, me := os.Stat(main)
		_, le := os.Stat(log)
		if errors.Is(me, os.ErrNotExist) && errors.Is(le, os.ErrNotExist) {
			continue
		}
		p := InspectionAction{Action: e.Name(), Log: log, Findings: []Finding{}, Errors: []string{}}
		var name string
		name, err = testPackage(main)
		if err != nil {
			p.Errors = append(p.Errors, err.Error())
		} else {
			p.Package = name
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
		if le != nil {
			p.Errors = append(p.Errors, "missing/unreadable testlog.txt: no coverage for this action (possibly a cache hit or disabled logging)")
		}
		r.Packages = append(r.Packages, p)
	}
	if len(r.Packages) == 0 {
		r.Errors = append(r.Errors, "no test actions/logs found; cache hits or disabled logging are not evidence of cache-independent tests")
		return r
	}
	if len(names) == 0 {
		return r
	}
	metadata, err := loadInspectionMetadata(c, names)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return r
	}
	for i := range r.Packages {
		p := &r.Packages[i]
		info, ok := metadata[p.Package]
		if p.Package == "" || !ok {
			if p.Package != "" {
				p.Errors = append(p.Errors, "package is absent from go list metadata")
			}
			continue
		}
		p.Dir, p.Root = info.Dir, info.Root
		if len(p.Errors) > 0 {
			continue
		}
		data, e := os.ReadFile(p.Log)
		if e != nil {
			p.Errors = append(p.Errors, e.Error())
			continue
		}
		l := ParseLog(data, info.Dir, info.Root, "")
		if l.Status != "complete" {
			p.Errors = append(p.Errors, l.Problem)
			continue
		}
		indices := map[string]int{}
		for _, rec := range l.Records {
			if !rec.CacheRelevant {
				p.Ignored++
				continue
			}
			key := rec.Operation + "\x00" + rec.Path
			if i, ok := indices[key]; ok {
				p.Findings[i].Count++
				continue
			}
			indices[key] = len(p.Findings)
			p.Findings = append(p.Findings, Finding{Package: p.Package, Rule: "TFS001", Operation: rec.Operation, Evidence: "observed", Confidence: "observed", Reason: "filesystem operation observed in preserved Go cache-input log; outcome and read/write mode are unavailable", Path: rec.Path, Class: rec.Class, CacheRelevant: true, Count: 1})
		}
	}
	sort.Slice(r.Packages, func(i, j int) bool {
		if r.Packages[i].Package != r.Packages[j].Package {
			return r.Packages[i].Package < r.Packages[j].Package
		}
		return r.Packages[i].Action < r.Packages[j].Action
	})
	return r
}
func testPackage(filename string) (string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		return "", fmt.Errorf("read generated test main: %w", err)
	}
	if f.Name.Name != "main" {
		return "", fmt.Errorf("%s: expected generated main package", filename)
	}
	alias := ""
	for _, imp := range f.Imports {
		path, e := strconv.Unquote(imp.Path.Value)
		if e == nil && path == "testing/internal/testdeps" {
			alias = "testdeps"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
		}
	}
	if alias == "" {
		return "", fmt.Errorf("%s: missing testing/internal/testdeps import", filename)
	}
	var names []string
	ast.Inspect(f, func(n ast.Node) bool {
		a, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range a.Lhs {
			s, ok := lhs.(*ast.SelectorExpr)
			if !ok || s.Sel.Name != "ImportPath" || i >= len(a.Rhs) {
				continue
			}
			id, ok := s.X.(*ast.Ident)
			if !ok || id.Name != alias {
				continue
			}
			lit, ok := a.Rhs[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			if n, e := strconv.Unquote(lit.Value); e == nil {
				names = append(names, n)
			}
		}
		return true
	})
	if len(names) != 1 || names[0] == "" {
		return "", fmt.Errorf("%s: missing/ambiguous package identity; expected Go 1.27 generated test main", filename)
	}
	return names[0], nil
}
func loadInspectionMetadata(c config, names []string) (map[string]inspectionPackage, error) {
	var data []byte
	var err error
	if c.PackagesJSON != "" {
		data, err = os.ReadFile(c.PackagesJSON)
	} else {
		cmd := exec.Command(c.Go, append(append([]string{"list", "-json"}, c.BuildFlags...), names...)...)
		cmd.Dir = c.Project
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		data, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list metadata: %w: %s (use original -project/build flags or -packages-json)", err, strings.TrimSpace(stderr.String()))
		}
	}
	if err != nil {
		return nil, err
	}
	out := map[string]inspectionPackage{}
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var p inspectionPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("invalid go list JSON: %w", err)
		}
		if p.Error != nil {
			return nil, fmt.Errorf("%s: %s", p.ImportPath, p.Error.Err)
		}
		if p.ImportPath == "" || !filepath.IsAbs(p.Dir) || (p.Root != "" && !filepath.IsAbs(p.Root)) {
			return nil, errors.New("metadata requires ImportPath, an absolute Dir, and an absolute or empty Root")
		}
		if old, ok := out[p.ImportPath]; ok && (old.Dir != p.Dir || old.Root != p.Root) {
			return nil, fmt.Errorf("conflicting metadata for %s", p.ImportPath)
		}
		out[p.ImportPath] = p
	}
	return out, nil
}
func printInspection(w io.Writer, r InspectionReport) {
	flagged, observations, incomplete := 0, 0, len(r.Errors)
	for _, e := range r.Errors {
		fmt.Fprintln(w, "ERROR:", e)
	}
	for _, p := range r.Packages {
		for _, e := range p.Errors {
			fmt.Fprintf(w, "ERROR %s (%s): %s\n", p.Package, p.Action, e)
			incomplete++
		}
		if len(p.Findings) == 0 {
			continue
		}
		flagged++
		fmt.Fprintf(w, "\n%s: potential CI test-cache instability [%s]\n", p.Package, p.Action)
		for _, f := range p.Findings {
			fmt.Fprintf(w, "  %s %q (%d occurrence(s))\n    %s\n", f.Operation, f.Path, f.Count, f.Reason)
			observations += f.Count
		}
	}
	fmt.Fprintf(w, "\n%d/%d inspected package action(s) flagged; %d cache-relevant operation(s); %d coverage error(s).\n", flagged, len(r.Packages), observations, incomplete)
	fmt.Fprintln(w, "Scope: preserved logs only; cache-hit packages may be absent. Findings are observations, not proven misses.")
}

var _ = report.SchemaVersion
