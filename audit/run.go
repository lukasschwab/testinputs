package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testfs/report"
)

const (
	ExitAuditFailure  = 2
	ExitPolicyFinding = 3
	launcherEnv       = "TESTFS_AUDIT_SESSION"
)

type Package struct {
	ImportPath string `json:"import_path"`
	Dir        string `json:"dir"`
	Root       string `json:"root"`
	HasTests   bool   `json:"has_tests"`
}
type Invocation struct {
	Package  string   `json:"package"`
	CWD      string   `json:"cwd"`
	Args     []string `json:"args"`
	Started  bool     `json:"started"`
	Finished bool     `json:"finished"`
	ExitCode int      `json:"exit_code"`
	Error    string   `json:"error,omitempty"`
	Log      Log      `json:"log"`
}
type Finding = report.Finding

type Report struct {
	SchemaVersion          int          `json:"schema_version"`
	GoVersion              string       `json:"go_version"`
	GOOS                   string       `json:"goos"`
	GOARCH                 string       `json:"goarch"`
	GoCommand              string       `json:"go_command"`
	Arguments              []string     `json:"arguments"`
	BuildFlags             []string     `json:"build_flags"`
	Packages               []Package    `json:"packages"`
	Invocations            []Invocation `json:"invocations"`
	Findings               []Finding    `json:"findings"`
	Limitations            []string     `json:"limitations"`
	Errors                 []string     `json:"errors"`
	TempBase               string       `json:"temp_base,omitempty"`
	TempEnvironmentChanged bool         `json:"temp_environment_changed"`
	LogDirectory           string       `json:"log_directory,omitempty"`
	TestExitCode           int          `json:"test_exit_code"`
	ExitCode               int          `json:"exit_code"`
}

type config struct {
	JSON, TempParent, Go        string
	Work, Project, PackagesJSON string
	BuildFlags                  repeatedValues
	KeepLogs, FailOnCheckout    bool
}

// Main implements testfs audit. Test failures retain the go command's status;
// collector failures use 2, and opt-in policy findings use 3.
func Main(args []string) int {
	var c config
	flags := flag.NewFlagSet("testfs audit", flag.ContinueOnError)
	flags.StringVar(&c.JSON, "json", "", "write the audit report to this JSON file")
	flags.StringVar(&c.TempParent, "temp-base", "", "create a dedicated temporary base beneath this external directory")
	flags.StringVar(&c.Go, "go", "go", "Go command to use")
	flags.StringVar(&c.Work, "work", "", "inspect this directory preserved by go test -work (does not execute tests)")
	flags.StringVar(&c.Project, "project", ".", "original project directory for inspection metadata")
	flags.StringVar(&c.PackagesJSON, "packages-json", "", "saved go list -json metadata for -work inspection")
	flags.Var(&c.BuildFlags, "build-flag", "go list build flag for -work inspection; repeatable")
	flags.BoolVar(&c.KeepLogs, "keep-logs", false, "retain raw collector logs and invocation metadata")
	flags.BoolVar(&c.FailOnCheckout, "fail-on-checkout", false, "exit 3 on observed cache-relevant checkout access")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return ExitAuditFailure
	}
	if c.Work != "" {
		return inspectMain(c, flags.Args())
	}
	if c.JSON == "-" {
		fmt.Fprintln(os.Stderr, "testfs audit: -json requires a file; stdout is reserved for go test")
		return ExitAuditFailure
	}
	if c.PackagesJSON != "" || len(c.BuildFlags) != 0 || c.Project != "." {
		fmt.Fprintln(os.Stderr, "testfs audit: -project, -packages-json, and -build-flag require -work")
		return ExitAuditFailure
	}
	r := run(c, flags.Args())
	if c.JSON != "" {
		data, err := json.MarshalIndent(r, "", "  ")
		if err == nil {
			err = os.WriteFile(c.JSON, append(data, '\n'), 0600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "testfs audit: writing report:", err)
			if r.TestExitCode == 0 {
				r.ExitCode = ExitAuditFailure
			}
		}
	}
	for _, err := range r.Errors {
		fmt.Fprintln(os.Stderr, "testfs audit:", err)
	}
	for _, f := range r.Findings {
		fmt.Fprintf(os.Stderr, "%s: %s observed %s (%s, %d occurrence(s))\n", f.Package, f.Operation, f.Path, f.Class, f.Count)
	}
	fmt.Fprintf(os.Stderr, "testfs audit: %d package execution(s), %d filesystem observation(s), %d collector error(s); this is not a cache-hit measurement\n", len(r.Invocations), len(r.Findings), len(r.Errors))
	if r.LogDirectory != "" {
		fmt.Fprintln(os.Stderr, "testfs audit: logs retained at", r.LogDirectory)
	}
	return r.ExitCode
}

func run(c config, args []string) (r Report) {
	r = Report{SchemaVersion: SchemaVersion, Arguments: args, Findings: []Finding{}, Invocations: []Invocation{}, Packages: []Package{}, Errors: []string{}, Limitations: append([]string(nil), CoverageLimits...)}
	fail := func(err error) Report {
		r.Errors = append(r.Errors, err.Error())
		r.ExitCode = ExitAuditFailure
		return r
	}
	listArgs, err := packageArgs(args)
	if err != nil {
		return fail(err)
	}
	goCommand, err := exec.LookPath(c.Go)
	if err != nil {
		return fail(err)
	}
	r.GoCommand = goCommand
	cmd := exec.Command(goCommand, "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOFLAGS")
	data, err := cmd.Output()
	if err != nil {
		return fail(fmt.Errorf("resolve Go toolchain: %w", err))
	}
	var env struct{ GOVERSION, GOOS, GOARCH, GOFLAGS string }
	if err = json.Unmarshal(data, &env); err != nil {
		return fail(err)
	}
	r.GoVersion, r.GOOS, r.GOARCH = env.GOVERSION, env.GOOS, env.GOARCH
	if !supportedVersion(env.GOVERSION, env.GOOS) {
		return fail(fmt.Errorf("collector supports Go 1.27.x on darwin, linux, and windows; selected %s/%s is unsupported", env.GOVERSION, env.GOOS))
	}
	goFlags, err := splitGoFlags(env.GOFLAGS)
	if err != nil {
		return fail(err)
	}
	if _, err = packageArgs(goFlags); err != nil {
		return fail(fmt.Errorf("GOFLAGS conflict: %w", err))
	}
	r.BuildFlags = append(append([]string(nil), goFlags...), listArgs...)
	cmd = exec.Command(goCommand, append([]string{"list", "-json"}, listArgs...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err = cmd.Output()
	if err != nil {
		return fail(fmt.Errorf("resolve package metadata: %w: %s", err, stderr.String()))
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var p struct {
			ImportPath, Dir, Root     string
			TestGoFiles, XTestGoFiles []string
			Module                    *struct{ Dir string }
		}
		if err = dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fail(err)
		}
		if p.Root == "" && p.Module != nil {
			p.Root = p.Module.Dir
		}
		r.Packages = append(r.Packages, Package{p.ImportPath, p.Dir, p.Root, len(p.TestGoFiles)+len(p.XTestGoFiles) > 0})
	}
	if len(r.Packages) == 0 {
		return fail(errors.New("package selection matched no packages"))
	}
	dir, err := os.MkdirTemp("", "testfs-audit-*")
	if err != nil {
		return fail(err)
	}
	if c.KeepLogs {
		r.LogDirectory = dir
	} else {
		defer os.RemoveAll(dir)
	}
	if c.TempParent != "" {
		parent, err := filepath.Abs(c.TempParent)
		if err != nil {
			return fail(err)
		}
		parent, err = filepath.EvalSymlinks(parent)
		if err != nil {
			return fail(err)
		}
		for _, p := range r.Packages {
			root := p.Root
			if real, err := filepath.EvalSymlinks(root); err == nil {
				root = real
			}
			if within(root, parent) {
				return fail(errors.New("dedicated temporary base must be outside all selected module/workspace roots"))
			}
		}
		r.TempBase, err = os.MkdirTemp(parent, "testfs-owned-*")
		if err != nil {
			return fail(err)
		}
		defer os.RemoveAll(r.TempBase)
		r.TempEnvironmentChanged = true
	}
	manifest := struct {
		Packages []Package
		TempBase string
	}{r.Packages, r.TempBase}
	if err = writeJSON(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return fail(err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	quoted, err := quoteExec(exe)
	if err != nil {
		return fail(err)
	}
	cmd = exec.Command(goCommand, append([]string{"test", "-count=1", "-exec", quoted + " --testfs-launch"}, args...)...)
	cmd.Env = append(os.Environ(), launcherEnv+"="+dir)
	r.TestExitCode = runProcess(cmd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "run-") {
			continue
		}
		base := filepath.Join(dir, entry.Name())
		var inv Invocation
		b, err := os.ReadFile(filepath.Join(base, "invocation.json"))
		if err == nil {
			err = json.Unmarshal(b, &inv)
		}
		if err != nil {
			r.Errors = append(r.Errors, "missing or malformed launcher metadata: "+err.Error())
			continue
		}
		var root string
		for _, p := range r.Packages {
			if p.ImportPath == inv.Package {
				root = p.Root
				seen[p.ImportPath] = true
				break
			}
		}
		inv.Log = readLog(filepath.Join(base, "actions.log"), inv.CWD, root, r.TempBase)
		if inv.ExitCode > 1 && inv.Log.Status == "complete" {
			inv.Log.Status = "partial"
			inv.Log.Problem = "abnormal test-process exit; buffered observations may be incomplete"
		}
		if !inv.Started || !inv.Finished || inv.Error != "" || inv.Package == "" || inv.Log.Status != "complete" {
			r.Errors = append(r.Errors, fmt.Sprintf("%s: incomplete collection (started=%t, finished=%t, log=%s): %s %s", inv.Package, inv.Started, inv.Finished, inv.Log.Status, inv.Error, inv.Log.Problem))
		}
		r.Invocations = append(r.Invocations, inv)
	}
	for _, p := range r.Packages {
		if p.HasTests && !seen[p.ImportPath] {
			r.Errors = append(r.Errors, p.ImportPath+": missing launcher invocation; audit coverage is incomplete")
		}
	}
	sort.Slice(r.Invocations, func(i, j int) bool { return r.Invocations[i].Package < r.Invocations[j].Package })
	r.Findings = aggregate(r.Invocations)
	if r.TestExitCode != 0 {
		r.ExitCode = r.TestExitCode
	} else if len(r.Errors) > 0 {
		r.ExitCode = ExitAuditFailure
	} else if c.FailOnCheckout {
		for _, f := range r.Findings {
			if f.Class == "checkout/module" && f.CacheRelevant {
				r.ExitCode = ExitPolicyFinding
				break
			}
		}
	}
	return r
}

func aggregate(invocations []Invocation) []Finding {
	out := []Finding{}
	for _, inv := range invocations {
		findings, _ := findingsFromRecords(inv.Package, inv.Log.Records, func(Record) bool { return true })
		out = append(out, findings...)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		return a.Path < b.Path
	})
	return out
}

// findingsFromRecords is shared by fresh collection and preserved-work
// inspection. selectRecord lets inspection retain Go cache inputs only.
func findingsFromRecords(pkg string, records []Record, selectRecord func(Record) bool) ([]Finding, int) {
	out, ignored := []Finding{}, 0
	indices := map[string]int{}
	for _, r := range records {
		if !selectRecord(r) {
			ignored++
			continue
		}
		key := r.Operation + "\x00" + r.Path
		if i, ok := indices[key]; ok {
			out[i].Count++
			continue
		}
		indices[key] = len(out)
		out = append(out, Finding{Package: pkg, Rule: "TFS001", Operation: r.Operation, Evidence: "observed", Confidence: "observed", Reason: "filesystem operation observed; outcome and read/write mode are unavailable", Path: r.Path, Class: r.Class, CacheRelevant: r.CacheRelevant, Count: 1})
	}
	return out, ignored
}

func supportedVersion(version, platform string) bool {
	if platform != "linux" && platform != "darwin" && platform != "windows" {
		return false
	}
	if !strings.HasPrefix(version, "go1.27.") {
		return false
	}
	patch := strings.TrimPrefix(version, "go1.27.")
	if patch == "" {
		return false
	}
	for _, r := range patch {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func writeJSON(filename string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(filename), "metadata-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filename)
}
