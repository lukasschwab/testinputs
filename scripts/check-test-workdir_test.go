package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestFilteringAndChdir(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "checkout")
	external := filepath.Join(base, "temp")
	info := packageInfo{ImportPath: "example.test/pkg", Dir: filepath.Join(root, "pkg"), Root: root}
	data := "# test log\ngetenv PRIVATE_VARIABLE\nopen fixture with spaces\nstat ../source.go\nopen " + filepath.Join(external, "owned") + "\nchdir " + external + "\nopen child\nstat " + filepath.Join(root, "go.mod") + "\n"
	findings, ignored, err := inspectLog([]byte(data), info)
	if err != nil {
		t.Fatal(err)
	}
	if ignored != 2 || len(findings) != 4 {
		t.Fatalf("findings=%+v ignored=%d", findings, ignored)
	}
	paths := map[string]string{}
	for _, f := range findings {
		paths[f.Operation+":"+f.Path] = f.Reason
	}
	for _, key := range []string{"open:" + filepath.Join(info.Dir, "fixture with spaces"), "stat:" + filepath.Join(root, "source.go"), "stat:" + filepath.Join(root, "go.mod"), "chdir:" + external} {
		if _, ok := paths[key]; !ok {
			t.Errorf("missing %s: %+v", key, findings)
		}
	}
	if !strings.Contains(paths["chdir:"+external], "even outside") {
		t.Fatal("missing chdir exception explanation")
	}
}

func TestNoRootAndTemporaryUnderCheckout(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "tmp", "test-owned")
	data := []byte("# test log\nopen " + p + "\nchdir " + root + "\n")
	for _, tt := range []struct {
		root              string
		findings, ignored int
	}{{"", 1, 1}, {root, 2, 0}} {
		got, ignored, err := inspectLog(data, packageInfo{Dir: root, Root: tt.root})
		if err != nil || len(got) != tt.findings || ignored != tt.ignored {
			t.Fatalf("%+v %d %v", got, ignored, err)
		}
	}
}

func TestLexicalAndSymlinkFiltering(t *testing.T) {
	base := t.TempDir()
	root, external := filepath.Join(base, "root"), filepath.Join(base, "outside")
	writeFile(t, filepath.Join(root, "file"), "inside")
	writeFile(t, filepath.Join(external, "file"), "outside")
	if cacheContains(root+"-sibling", root) {
		t.Fatal("sibling prefix included")
	}
	if !cacheContains(root, root) {
		t.Fatal("root itself excluded")
	}
	// An absolute name is not cleaned before Go's lexical prefix check.
	unclean := root + string(filepath.Separator) + ".." + string(filepath.Separator) + "outside" + string(filepath.Separator) + "file"
	if !cacheContains(unclean, root) {
		t.Fatal("absolute spelling must retain lexical inclusion")
	}
	linkOut, linkIn, rootAlias := filepath.Join(root, "out"), filepath.Join(external, "in"), filepath.Join(base, "alias")
	if err := os.Symlink(external, linkOut); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(root, linkIn); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, rootAlias); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{filepath.Join(linkOut, "file"), root}, // lexical inclusion wins despite escape
		{filepath.Join(linkIn, "file"), root},  // external alias resolves into root
		{filepath.Join(root, "file"), rootAlias},
		{filepath.Join(linkIn, "file"), rootAlias},
	} {
		if !cacheContains(pair[0], pair[1]) {
			t.Errorf("unexpected exclusion: %q in %q", pair[0], pair[1])
		}
	}
	if cacheContains(filepath.Join(external, "file"), root) {
		t.Fatal("external file included")
	}
	if cacheContains(filepath.Join(linkIn, "missing"), root) {
		t.Fatal("failed symlink resolution must not infer containment")
	}
}

func TestWindowsVolumeCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows paths")
	}
	if !lexicalContains(`c:\repo\file`, `C:\repo`) {
		t.Fatal("volume case should not matter")
	}
	if lexicalContains(`C:\Repo\file`, `C:\repo`) {
		t.Fatal("non-volume case must matter, matching Go")
	}
	if lexicalContains(`C:\repo/file`, `C:\repo`) {
		t.Fatal("Go's raw lexical check requires native separators")
	}
}

func TestLogValidationAndAggregation(t *testing.T) {
	root := t.TempDir()
	info := packageInfo{Dir: root, Root: root}
	for _, data := range []string{"", "not a log\n", "# test log\nopen truncated", "# test log\nmalformed\n", "# test log\nread file\n", "# test log\nchdir relative\n"} {
		if _, _, err := inspectLog([]byte(data), info); err == nil {
			t.Errorf("accepted %q", data)
		}
	}
	data := "# test log\n\nopen a b\nopen ./a b\nopen a b\n"
	got, _, err := inspectLog([]byte(data), info)
	if err != nil || len(got) != 1 || got[0].Count != 3 || len(got[0].RawPaths) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, _, err := inspectLog([]byte("# test log\n"), info); err != nil {
		t.Fatal(err)
	}
}

func generatedMain(name string) string {
	return fmt.Sprintf("package main\nimport deps \"testing/internal/testdeps\"\nfunc init(){deps.ImportPath=%q}\n", name)
}

func TestWorkDirectoryCLI(t *testing.T) {
	work, root := t.TempDir(), t.TempDir()
	for _, tt := range []struct{ action, name, log string }{
		{"b001", "example.test/a", "# test log\nopen source.go\n"},
		{"b002", "example.test/b", "# test log\n"},
	} {
		writeFile(t, filepath.Join(work, tt.action, "_testmain.go"), generatedMain(tt.name))
		writeFile(t, filepath.Join(work, tt.action, "testlog.txt"), tt.log)
	}
	meta := filepath.Join(t.TempDir(), "packages.json")
	a, _ := json.Marshal(packageInfo{ImportPath: "example.test/a", Dir: root, Root: root})
	b, _ := json.Marshal(packageInfo{ImportPath: "example.test/b", Dir: root, Root: root})
	writeFile(t, meta, string(a)+"\n"+string(b))
	var out, stderr bytes.Buffer
	code := mainWithArgs([]string{"-work", work, "-packages-json", meta, "-json"}, &out, &stderr)
	if code != 1 {
		t.Fatalf("code=%d output=%s stderr=%s", code, &out, &stderr)
	}
	var r workReport
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Packages) != 2 || len(r.Packages[0].Findings) != 1 || len(r.Packages[1].Findings) != 0 {
		t.Fatalf("%+v", r)
	}
	writeFile(t, filepath.Join(work, "b003", "_testmain.go"), generatedMain("example.test/a"))
	out.Reset()
	if code := mainWithArgs([]string{"-packages-json", meta, work}, &out, &stderr); code != 2 || !strings.Contains(out.String(), "missing/unreadable") {
		t.Fatalf("%d %s", code, &out)
	}
	writeFile(t, filepath.Join(work, "b003", "testlog.txt"), "# test log\n")
	writeFile(t, filepath.Join(work, "b001", "testlog.txt"), "# test log\n")
	if code := mainWithArgs([]string{"-packages-json", meta, work}, &out, &stderr); code != 0 {
		t.Fatalf("clean logs: %d %s", code, &out)
	}
	if code := mainWithArgs([]string{t.TempDir()}, &out, &stderr); code != 2 {
		t.Fatal("empty work reported clean")
	}
}

func TestMissingIdentityAndMetadata(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "b001", "testlog.txt"), "# test log\nopen fixture\n")
	r := inspectWork(options{work: work})
	if len(r.Packages) != 1 || len(r.Packages[0].Errors) == 0 {
		t.Fatalf("%+v", r)
	}
	main := filepath.Join(work, "b001", "_testmain.go")
	writeFile(t, main, generatedMain(""))
	if _, err := testPackage(main); err == nil {
		t.Fatal("empty identity accepted")
	}
	writeFile(t, main, generatedMain("example.test/a"))
	for _, data := range []string{"not json", `{"ImportPath":"a","Dir":"relative"}`, `{"ImportPath":"a","Error":{"Err":"unavailable"}}`} {
		meta := filepath.Join(t.TempDir(), "packages.json")
		writeFile(t, meta, data)
		if _, err := loadMetadata(options{metadata: meta}, nil); err == nil {
			t.Errorf("accepted metadata %q", data)
		}
	}
}

// workdirGolden is a captured Go 1.27.0 linux/amd64 preserved work
// directory. It keeps normal tests independent of both `go test -work` and
// `go list`; expected inputs came from Go's GODEBUG=gocachehash=1 output.
//
//go:embed all:testdata/workdir/linux
var workdirGolden embed.FS

func materializeGolden(t *testing.T, dst, root, external, temporary string) {
	t.Helper()
	err := fs.WalkDir(workdirGolden, "testdata/workdir/linux", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := workdirGolden.ReadFile(name)
		if err != nil {
			return err
		}
		data = []byte(strings.NewReplacer("@ROOT@", root, "@EXTERNAL@", external, "@TEMP@", temporary).Replace(string(data)))
		path := filepath.Join(dst, strings.TrimPrefix(name, "testdata/workdir/linux/"))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCapturedWorkDirectoryMatchesCacheInputs(t *testing.T) {
	base := t.TempDir()
	work, root := filepath.Join(base, "work"), filepath.Join(base, "checkout with spaces")
	external, temporary := filepath.Join(base, "external"), filepath.Join(base, "temporary")
	for _, dir := range []string{work, root, filepath.Join(root, "second"), external, temporary} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	materializeGolden(t, work, root, external, temporary)
	meta := filepath.Join(work, "packages.json")
	r := inspectWork(options{work: work, metadata: meta})
	if len(r.Errors) != 0 || len(r.Packages) != 2 {
		t.Fatalf("%+v", r)
	}
	got := map[string]bool{}
	for _, p := range r.Packages {
		if len(p.Errors) != 0 {
			t.Fatalf("%+v", p)
		}
		for _, f := range p.Findings {
			got[f.Operation+" "+f.Path] = true
		}
	}
	wantData, err := workdirGolden.ReadFile("testdata/workdir/linux/expected-inputs.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, line := range strings.Split(string(wantData), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			want[strings.NewReplacer("@ROOT@", root, "@EXTERNAL@", external, "@TEMP@", temporary).Replace(line)] = true
		}
	}
	if !mapsEqual(got, want) {
		t.Fatalf("cache input mismatch\nscript: %q\nGo golden: %q", sortedSet(got), sortedSet(want))
	}
	if r.Packages[0].Ignored+r.Packages[1].Ignored < 3 {
		t.Fatalf("external/temporary operations were not filtered: %+v", r.Packages)
	}
	// Saved metadata means preserved-work inspection neither runs go test nor go list.
	before, err := os.ReadFile(filepath.Join(work, "b001", "testlog.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := mainWithArgs([]string{"-work", work, "-packages-json", meta}, &out, &stderr); code != 1 {
		t.Fatalf("code=%d\n%s\n%s", code, &out, &stderr)
	}
	after, err := os.ReadFile(filepath.Join(work, "b001", "testlog.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("inspection modified test log")
	}
}

func mapsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if !b[key] {
			return false
		}
	}
	return true
}

func sortedSet(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
