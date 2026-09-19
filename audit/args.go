package audit

import (
	"fmt"
	"strings"
)

var buildValues = map[string]bool{"tags": true, "mod": true, "modfile": true, "overlay": true, "compiler": true, "gccgoflags": true, "gcflags": true, "asmflags": true, "ldflags": true, "pkgdir": true, "pgo": true, "toolexec": true, "buildvcs": true}
var buildBools = map[string]bool{"race": true, "msan": true, "asan": true, "cover": true, "modcacherw": true, "trimpath": true, "a": true, "n": true, "x": true, "work": true}
var testValues = map[string]bool{"run": true, "skip": true, "timeout": true, "parallel": true, "cpu": true, "shuffle": true, "count": true, "bench": true, "benchtime": true, "fuzz": true, "fuzztime": true, "fuzzminimizetime": true, "list": true, "coverprofile": true, "covermode": true, "coverpkg": true, "blockprofile": true, "blockprofilerate": true, "cpuprofile": true, "memprofile": true, "memprofilerate": true, "mutexprofile": true, "mutexprofilefraction": true, "outputdir": true, "trace": true, "p": true, "vet": true}

func flagName(s string) (string, string, bool) {
	s = strings.TrimLeft(s, "-")
	s = strings.TrimPrefix(s, "test.")
	return strings.Cut(s, "=")
}

// packageArgs extracts package selection and build settings for go list. It
// rejects flags that could hide execution or override the collector contract.
func packageArgs(args []string) ([]string, error) {
	var build, packages []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-args" || a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			packages = append(packages, a)
			continue
		}
		name, value, eq := flagName(a)
		if name == "exec" || name == "testlogfile" || name == "c" || name == "o" || name == "list" {
			return nil, fmt.Errorf("audit does not support conflicting or non-executing flag %s", a)
		}
		needsValue := buildValues[name] || testValues[name]
		if needsValue && !eq {
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("missing value for %s", a)
			}
			value = args[i]
		}
		if name == "count" {
			return nil, fmt.Errorf("audit owns -count=1; remove %s", a)
		}
		if (name == "fuzz" || name == "bench") && value != "" {
			return nil, fmt.Errorf("active fuzzing and benchmarks are outside audit coverage")
		}
		if name == "n" {
			return nil, fmt.Errorf("audit requires test execution; -n is unsupported")
		}
		if buildValues[name] || buildBools[name] || name == "covermode" || name == "coverpkg" || name == "p" {
			build = append(build, a)
			if needsValue && !eq {
				build = append(build, value)
			}
		}
	}
	// Also reject collisions passed directly to the test binary after -args.
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, _, _ := flagName(a)
		if name == "exec" || name == "testlogfile" || name == "count" || name == "fuzz" || name == "bench" || name == "list" {
			return nil, fmt.Errorf("audit owns execution flags; unsupported argument %s", a)
		}
	}
	if len(packages) == 0 {
		packages = []string{"."}
	}
	return append(build, packages...), nil
}

// Go's -exec parser accepts single- or double-quoted words, not shell escapes.
func quoteExec(s string) (string, error) {
	if !strings.Contains(s, `"`) {
		return `"` + s + `"`, nil
	}
	if !strings.Contains(s, `'`) {
		return `'` + s + `'`, nil
	}
	return "", fmt.Errorf("executable path contains both quote characters and cannot be represented in go test -exec")
}

func splitGoFlags(s string) ([]string, error) {
	var out []string
	for len(strings.TrimSpace(s)) > 0 {
		s = strings.TrimSpace(s)
		if s[0] == '\'' || s[0] == '"' {
			q := s[0]
			end := strings.IndexByte(s[1:], q)
			if end < 0 {
				return nil, fmt.Errorf("unterminated quote in GOFLAGS")
			}
			out = append(out, s[1:end+1])
			s = s[end+2:]
			continue
		}
		end := strings.IndexAny(s, " \t\r\n")
		if end < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:end])
		s = s[end:]
	}
	return out, nil
}
