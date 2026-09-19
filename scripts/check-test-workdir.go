// check-test-workdir inspects the dependency logs retained by go test -work.
// Run with: go run /path/to/check-test-workdir.go -work /path/to/go-build123
// This file intentionally uses only the standard library so it can be copied
// and run independently of the testfs module.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
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
)

const filterModel = "go1.27"

type packageInfo struct {
	ImportPath string
	Dir        string
	Root       string
	Error      *struct{ Err string }
}

type finding struct {
	Operation string   `json:"operation"`
	Path      string   `json:"path"`
	RawPaths  []string `json:"raw_paths"`
	Count     int      `json:"count"`
	Reason    string   `json:"reason"`
	Advice    string   `json:"advice"`
}

type packageReport struct {
	Package  string    `json:"package"`
	Dir      string    `json:"dir"`
	Root     string    `json:"root"`
	Action   string    `json:"action"`
	Log      string    `json:"log"`
	Findings []finding `json:"findings"`
	Ignored  int       `json:"ignored_filesystem_operations"`
	Errors   []string  `json:"errors"`
}

type workReport struct {
	SchemaVersion int             `json:"schema_version"`
	FilterModel   string          `json:"filter_model"`
	Work          string          `json:"work"`
	Packages      []packageReport `json:"packages"`
	Errors        []string        `json:"errors"`
	Limitations   []string        `json:"limitations"`
}

type options struct {
	work, project, metadata, goCommand string
	buildFlags                         []string
	json                               bool
}

type repeatedFlags []string

func (v *repeatedFlags) String() string     { return strings.Join(*v, ", ") }
func (v *repeatedFlags) Set(s string) error { *v = append(*v, s); return nil }

func main() { os.Exit(mainWithArgs(os.Args[1:], os.Stdout, os.Stderr)) }

func mainWithArgs(args []string, stdout, stderr io.Writer) int {
	var opts options
	var buildFlags repeatedFlags
	flags := flag.NewFlagSet("check-test-workdir", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.work, "work", "", "preserved directory printed by go test -work")
	flags.StringVar(&opts.project, "project", ".", "original project directory used to resolve package metadata")
	flags.StringVar(&opts.metadata, "packages-json", "", "read saved go list -json output instead of invoking Go")
	flags.StringVar(&opts.goCommand, "go", "go", "Go command used for package metadata")
	flags.Var(&buildFlags, "build-flag", "go list build flag, e.g. -build-flag=-tags=integration; repeatable")
	flags.BoolVar(&opts.json, "json", false, "write JSON to stdout")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if opts.work == "" && flags.NArg() == 1 {
		opts.work = flags.Arg(0)
	} else if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "supply exactly one work directory using -work or a positional argument")
		return 2
	}
	if opts.work == "" {
		fmt.Fprintln(stderr, "-work is required; first run go test -work ./...")
		return 2
	}
	opts.buildFlags = buildFlags
	r := inspectWork(opts)
	if opts.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	} else {
		printReport(stdout, r)
	}
	code := 0
	for _, p := range r.Packages {
		if len(p.Findings) > 0 {
			code = 1
		}
	}
	if len(r.Errors) > 0 {
		code = 2
	}
	for _, p := range r.Packages {
		if len(p.Errors) > 0 {
			code = 2
		}
	}
	return code
}

func inspectWork(opts options) workReport {
	r := workReport{SchemaVersion: 1, FilterModel: filterModel, Packages: []packageReport{}, Errors: []string{}, Limitations: []string{
		"Reports package-level cache risks, not individual test attribution or reproduced cache misses.",
		"Only preserved logs are inspected; cache hits or skipped packages may leave no logs or actions here.",
		"Package directories, roots, and symlinks must match the original execution; metadata is resolved now unless supplied with -packages-json.",
		"Filtering models Go 1.27 computeTestInputsID/search.InDir; it does not compute hashes, check cache eligibility, or inspect environment inputs.",
		"Open records include failed and write-mode opens. Initialization, pre-m.Run setup, subprocesses, and direct syscalls may be unlogged.",
	}}
	abs, err := filepath.Abs(opts.work)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return r
	}
	r.Work = abs
	entries, err := os.ReadDir(abs)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return r
	}
	// Each bNNN action has its own generated main and optional test log. Never
	// infer a package from the binary basename, which is not globally unique.
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		action := filepath.Join(abs, entry.Name())
		mainFile, logFile := filepath.Join(action, "_testmain.go"), filepath.Join(action, "testlog.txt")
		_, mainErr := os.Stat(mainFile)
		_, logErr := os.Stat(logFile)
		if errors.Is(mainErr, os.ErrNotExist) && errors.Is(logErr, os.ErrNotExist) {
			continue
		}
		p := packageReport{Action: entry.Name(), Log: logFile, Findings: []finding{}, Errors: []string{}}
		name, err := testPackage(mainFile)
		if err != nil {
			p.Errors = append(p.Errors, err.Error())
		} else {
			p.Package = name
		}
		if logErr != nil {
			p.Errors = append(p.Errors, "missing/unreadable testlog.txt: no coverage for this action (possibly a cache hit or disabled logging)")
		}
		r.Packages = append(r.Packages, p)
	}
	if len(r.Packages) == 0 {
		r.Errors = append(r.Errors, "no test actions/logs found; cache hits or disabled logging are not evidence of cache-independent tests")
		return r
	}
	var names []string
	seen := map[string]bool{}
	for _, p := range r.Packages {
		if p.Package != "" && !seen[p.Package] {
			names = append(names, p.Package)
			seen[p.Package] = true
		}
	}
	if len(names) == 0 {
		return r
	}
	metadata, err := loadMetadata(opts, names)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return r
	}
	for i := range r.Packages {
		p := &r.Packages[i]
		if p.Package == "" {
			continue
		}
		info, ok := metadata[p.Package]
		if !ok {
			p.Errors = append(p.Errors, "package is absent from go list metadata")
			continue
		}
		p.Dir, p.Root = info.Dir, info.Root
		if len(p.Errors) > 0 {
			continue
		}
		data, err := os.ReadFile(p.Log)
		if err != nil {
			p.Errors = append(p.Errors, err.Error())
			continue
		}
		p.Findings, p.Ignored, err = inspectLog(data, info)
		if err != nil {
			p.Errors = append(p.Errors, err.Error())
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
		path, err := strconv.Unquote(imp.Path.Value)
		if err == nil && path == "testing/internal/testdeps" {
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
			name, err := strconv.Unquote(lit.Value)
			if err == nil {
				names = append(names, name)
			}
		}
		return true
	})
	if len(names) != 1 || names[0] == "" {
		return "", fmt.Errorf("%s: missing/ambiguous package identity; expected Go 1.27 generated test main", filename)
	}
	return names[0], nil
}

func loadMetadata(opts options, names []string) (map[string]packageInfo, error) {
	var data []byte
	var err error
	if opts.metadata != "" {
		data, err = os.ReadFile(opts.metadata)
	} else {
		args := append([]string{"list", "-json"}, opts.buildFlags...)
		args = append(args, names...)
		cmd := exec.Command(opts.goCommand, args...)
		cmd.Dir = opts.project
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		data, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list metadata: %w: %s (use the original -project/build flags or -packages-json)", err, strings.TrimSpace(stderr.String()))
		}
	}
	if err != nil {
		return nil, err
	}
	result := map[string]packageInfo{}
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var p packageInfo
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("invalid go list JSON: %w", err)
		}
		if p.Error != nil {
			return nil, fmt.Errorf("%s: %s", p.ImportPath, p.Error.Err)
		}
		if p.ImportPath == "" || !filepath.IsAbs(p.Dir) || p.Root != "" && !filepath.IsAbs(p.Root) {
			return nil, errors.New("metadata requires ImportPath, an absolute Dir, and an absolute or empty Root")
		}
		if old, ok := result[p.ImportPath]; ok && (old.Dir != p.Dir || old.Root != p.Root) {
			return nil, fmt.Errorf("conflicting metadata for %s", p.ImportPath)
		}
		result[p.ImportPath] = p
	}
	return result, nil
}

func inspectLog(data []byte, info packageInfo) ([]finding, int, error) {
	out := []finding{}
	if !bytes.HasPrefix(data, []byte("# test log\n")) {
		return out, 0, errors.New("invalid test log header")
	}
	if data[len(data)-1] != '\n' {
		return out, 0, errors.New("truncated test log: final newline is missing")
	}
	cwd, ignored := info.Dir, 0
	indices := map[string]int{}
	for i, line := range strings.Split(string(data[len("# test log\n"):]), "\n") {
		// Go's input computation ignores empty lines.
		if line == "" {
			continue
		}
		op, name, ok := strings.Cut(line, " ")
		if !ok {
			return out, ignored, fmt.Errorf("malformed record %d", i+1)
		}
		if op == "getenv" {
			continue
		} // Do not read or expose environment values.
		switch op {
		case "open", "stat":
			// Go cleans relative paths via Join, but retains absolute spelling
			// for its lexical containment check (including any .. components).
			if !filepath.IsAbs(name) {
				name = filepath.Join(cwd, name)
			}
			if info.Root == "" || !cacheContains(name, info.Root) {
				ignored++
				continue
			}
		case "chdir":
			// Go's logger records successful chdir as an absolute path. Reject
			// other forms instead of inventing a cwd Go would not have used.
			if !filepath.IsAbs(name) {
				return out, ignored, fmt.Errorf("record %d: chdir must be absolute", i+1)
			}
			cwd = name
		default:
			return out, ignored, fmt.Errorf("record %d: unsupported operation %q", i+1, op)
		}
		key := op + "\x00" + name
		if idx, ok := indices[key]; ok {
			out[idx].Count++
			_, raw, _ := strings.Cut(line, " ")
			if !contains(out[idx].RawPaths, raw) {
				out[idx].RawPaths = append(out[idx].RawPaths, raw)
			}
			continue
		}
		reason := "Go includes this filesystem observation in its cache inputs; a fresh checkout may change metadata without changing file contents"
		advice := "embed fixture data, or write embedded data into t.TempDir() before exercising filesystem code; document intentional source checks"
		if op == "chdir" {
			reason = "Go hashes chdir directory metadata even outside the package root; fresh or temporary directories may destabilize caching"
			advice = "prefer explicit paths over changing the working directory; t.Chdir(t.TempDir()) is not exempt"
		} else if op == "open" {
			reason += "; opening a directory also hashes its entries (the record does not distinguish reads, writes, or failed opens)"
		}
		_, raw, _ := strings.Cut(line, " ")
		indices[key] = len(out)
		out = append(out, finding{Operation: op, Path: name, RawPaths: []string{raw}, Count: 1, Reason: reason, Advice: advice})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Operation != out[j].Operation {
			return out[i].Operation < out[j].Operation
		}
		return out[i].Path < out[j].Path
	})
	return out, ignored, nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// cacheContains mirrors Go 1.27's search.InDir: preserve lexical containment
// before trying symlink resolution. Resolving both paths first would incorrectly
// exclude an in-root symlink to an external file. No internal Go imports needed.
func cacheContains(name, root string) bool {
	if root == "" {
		return false
	}
	if lexicalContains(name, root) {
		return true
	}
	realName, err := filepath.EvalSymlinks(name)
	if err != nil || realName == name {
		realName = ""
	} else if lexicalContains(realName, root) {
		return true
	}
	realRoot, err := filepath.EvalSymlinks(root)
	return err == nil && realRoot != root && (lexicalContains(name, realRoot) || realName != "" && lexicalContains(realName, realRoot))
}

func lexicalContains(name, root string) bool {
	// Go compares volume names case-insensitively on Windows, while comparing
	// the remainder case-sensitively, even on a case-insensitive filesystem.
	nv, rv := filepath.VolumeName(name), filepath.VolumeName(root)
	if strings.ToUpper(nv) != strings.ToUpper(rv) {
		return false
	}
	name, root = name[len(nv):], root[len(rv):]
	if name == root || root == "" {
		return true
	}
	if !strings.HasPrefix(name, root) {
		return false
	}
	return root[len(root)-1] == filepath.Separator || len(name) > len(root) && name[len(root)] == filepath.Separator
}

func printReport(w io.Writer, r workReport) {
	flagged, observations, incomplete := 0, 0, len(r.Errors)
	for _, err := range r.Errors {
		fmt.Fprintln(w, "ERROR:", err)
	}
	for _, p := range r.Packages {
		for _, err := range p.Errors {
			fmt.Fprintf(w, "ERROR %s (%s): %s\n", p.Package, p.Action, err)
			incomplete++
		}
		if len(p.Findings) == 0 {
			continue
		}
		flagged++
		fmt.Fprintf(w, "\n%s: potential CI test-cache instability [%s]\n", p.Package, p.Action)
		for _, f := range p.Findings {
			fmt.Fprintf(w, "  %s %q (%d occurrence(s))\n    %s\n    Advice: %s\n", f.Operation, f.Path, f.Count, f.Reason, f.Advice)
			observations += f.Count
		}
	}
	fmt.Fprintf(w, "\n%d/%d inspected package action(s) flagged; %d cache-relevant operation(s); %d coverage error(s).\n", flagged, len(r.Packages), observations, incomplete)
	fmt.Fprintln(w, "Scope: preserved logs only; cache-hit packages may be absent. Findings are package-level cache risks, not proven misses.")
}
