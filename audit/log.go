// Package audit collects package-level filesystem observations from go test.
// Its backend uses an explicitly version-gated internal Go test log format.
package audit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testfs/report"
)

const SchemaVersion = report.SchemaVersion

var CoverageLimits = []string{
	"This is an uncached audit (-count=1), not a test-cache hit or miss measurement.",
	"Open records are attempted filesystem accesses; the log contains no result or read/write mode.",
	"Records have no test name, source position, stack, or goroutine attribution.",
	"Logging starts during m.Run: package initialization and TestMain setup before m.Run are not covered.",
	"Child-process, direct-syscall, and C-library I/O bypassing Go hooks is not covered.",
	"Empty names and names containing newlines are omitted; crashes can lose buffered records.",
	"Symlinks and concurrent working-directory changes can make path classification ambiguous.",
	"A syntactically valid log cannot prove that every buffered operation was flushed.",
}

type Record struct {
	Sequence      int    `json:"sequence"`
	Operation     string `json:"operation"`
	RawPath       string `json:"raw_path"`
	Path          string `json:"path"`
	RealPath      string `json:"real_path,omitempty"`
	Class         string `json:"class"`
	CacheRelevant bool   `json:"cache_relevant"`
	Ambiguous     bool   `json:"ambiguous,omitempty"`
}

type Log struct {
	Status  string   `json:"status"`
	Problem string   `json:"problem,omitempty"`
	Records []Record `json:"records"`
}

// ParseLog retains record order, splits only the first space, and deliberately
// drops getenv records. No environment values are collected or exposed.
func ParseLog(data []byte, cwd, root, tempBase string) Log {
	l := Log{Status: "complete", Records: []Record{}}
	if !bytes.HasPrefix(data, []byte("# test log\n")) {
		l.Status = "malformed"
		l.Problem = "missing or invalid test log header"
		return l
	}
	if data[len(data)-1] != '\n' {
		l.Status = "partial"
		l.Problem = "unterminated final record"
	}
	lines := strings.Split(string(data[len("# test log\n"):]), "\n")
	// Drop the trailing empty entry or the incomplete final record.
	lines = lines[:len(lines)-1]
	for i, line := range lines {
		// computeTestInputsID ignores blank records.
		if line == "" {
			continue
		}
		op, name, ok := strings.Cut(line, " ")
		if !ok || name == "" || (op != "getenv" && op != "open" && op != "stat" && op != "chdir") {
			l.Status = "malformed"
			l.Problem = fmt.Sprintf("invalid record %d", i+1)
			continue
		}
		if op == "getenv" {
			continue
		}
		if op == "chdir" && !filepath.IsAbs(name) {
			l.Status = "malformed"
			l.Problem = fmt.Sprintf("record %d: chdir must be absolute", i+1)
			continue
		}
		// Go cleans relative names through Join, but intentionally retains an
		// absolute name's lexical spelling for search.InDir.
		p := name
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		displayPath := filepath.Clean(p)
		r := Record{Sequence: i + 1, Operation: op, RawPath: name, Path: p, Class: "external/cache-ignored", CacheRelevant: cacheContains(root, p) || op == "chdir"}
		r.Ambiguous = filepath.VolumeName(name) != "" && !filepath.IsAbs(name)
		if r.CacheRelevant {
			r.Class = "external"
		}
		if within(root, displayPath) {
			r.Class = "checkout/module"
		}
		if tempBase != "" && within(tempBase, displayPath) {
			r.Class = "dedicated-temporary"
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			r.RealPath = real
			realRoot := root
			if resolved, err := filepath.EvalSymlinks(root); err == nil {
				realRoot = resolved
			}
			if within(realRoot, real) {
				r.Class = "checkout/module"
			} else if within(root, p) {
				r.Class = "external"
			}
			if tempBase != "" {
				realTemp := tempBase
				if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
					realTemp = resolved
				}
				if within(realTemp, real) {
					r.Class = "dedicated-temporary"
				} else if r.Class == "dedicated-temporary" {
					r.Class = "external"
				}
			}
			if real != p {
				r.Ambiguous = true
			}
		}
		if op == "chdir" {
			cwd = p
		}
		l.Records = append(l.Records, r)
	}
	return l
}

// Match the Go 1.27 search.InDir cache-root check: lexical containment first,
// then symlink fallbacks. Display classification is kept separate above.
func cacheContains(root, name string) bool {
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
	nv, rv := filepath.VolumeName(name), filepath.VolumeName(root)
	if !strings.EqualFold(nv, rv) {
		return false
	}
	name, root = name[len(nv):], root[len(rv):]
	if name == root || root == "" {
		return true
	}
	return strings.HasPrefix(name, root) && (root[len(root)-1] == filepath.Separator || len(name) > len(root) && name[len(root)] == filepath.Separator)
}

func readLog(filename, cwd, root, tempBase string) Log {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Log{Status: "missing", Problem: err.Error(), Records: []Record{}}
	}
	return ParseLog(data, cwd, root, tempBase)
}

func within(root, name string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, name)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
