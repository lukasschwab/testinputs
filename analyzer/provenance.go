package analyzer

import (
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
)

// Expressions are a bounded, serializable, parameterized provenance language.
// Retaining transformations until call-site substitution is essential: joining
// a parameter is only safe once its actual root and components are known.
type expr struct {
	Kind  string
	Text  string
	Index int
	Args  []expr
}

func (x expr) key() string                { b, _ := json.Marshal(x); return string(b) }
func leaf(kind string) expr               { return expr{Kind: kind} }
func literal(s string) expr               { return expr{Kind: "literal", Text: s} }
func node(kind string, args ...expr) expr { return expr{Kind: kind, Args: args} }
func union(xs ...expr) expr {
	var out []expr
	seen := map[string]bool{}
	var add func(expr)
	add = func(x expr) {
		if x.Kind == "" {
			return
		}
		if x.Kind == "union" {
			for _, a := range x.Args {
				add(a)
			}
			return
		}
		k := x.key()
		if !seen[k] {
			seen[k] = true
			out = append(out, x)
		}
	}
	for _, x := range xs {
		add(x)
	}
	if len(out) == 0 {
		return expr{}
	}
	if len(out) == 1 {
		return out[0]
	}
	return node("union", out...)
}

func substitute(x expr, args []expr) expr {
	if x.Kind == "param" {
		if x.Index < len(args) {
			return args[x.Index]
		}
		return leaf("unknown")
	}
	if len(x.Args) == 0 {
		return x
	}
	out := x
	out.Args = make([]expr, len(x.Args))
	for i, a := range x.Args {
		out.Args[i] = substitute(a, args)
	}
	if out.Kind == "union" {
		return union(out.Args...)
	}
	if out.Kind == "field" && len(out.Args) == 1 {
		return field(out.Args[0], out.Index)
	}
	return out
}

func field(x expr, index int) expr {
	if x.Kind == "struct" || x.Kind == "tuple" || x.Kind == "array" {
		if index < len(x.Args) {
			return x.Args[index]
		}
		return leaf("unknown")
	}
	if x.Kind == "union" {
		var out []expr
		for _, a := range x.Args {
			out = append(out, field(a, index))
		}
		return union(out...)
	}
	return expr{Kind: "field", Index: index, Args: []expr{x}}
}

type origins uint8

const (
	embedded origins = 1 << iota
	memory
	temporary
	disk
	unknown
)

func classify(x expr) origins {
	switch x.Kind {
	case "embed":
		return embedded
	case "memory":
		return memory
	case "temp":
		return temporary
	case "literal", "disk", "systemtemp":
		return disk
	case "union":
		var out origins
		for _, a := range x.Args {
			out |= classify(a)
		}
		return out
	case "dirfs", "handle", "sub", "root":
		if len(x.Args) > 0 {
			return classify(x.Args[0])
		}
	case "tempcreate":
		if len(x.Args) > 1 {
			parent := x.Args[0]
			pattern, ok := constantString(x.Args[1])
			if !ok || strings.ContainsAny(pattern, "/\\") {
				return unknown
			}
			if parent.Kind == "systemtemp" || parent.Kind == "literal" && parent.Text == "" {
				return temporary
			}
			return classify(parent)
		}
	case "join", "concat":
		if s, ok := constantString(x); ok {
			_ = s
			return disk
		}
		if len(x.Args) == 0 {
			return unknown
		}
		base := classify(x.Args[0])
		if base&temporary == 0 {
			return base
		}
		var parts []string
		for _, a := range x.Args[1:] {
			s, ok := constantString(a)
			if !ok {
				return (base &^ temporary) | unknown
			}
			parts = append(parts, s)
		}
		suffix := strings.Join(parts, "/")
		if x.Kind == "concat" {
			suffix = strings.Join(parts, "")
			if !strings.HasPrefix(suffix, "/") && !strings.HasPrefix(suffix, "\\") {
				return (base &^ temporary) | unknown
			}
			suffix = strings.TrimLeft(suffix, "/\\")
		}
		// Accept only relative, contained components across Unix and Windows.
		if strings.HasPrefix(suffix, "/") || strings.ContainsAny(suffix, "\\:") {
			return (base &^ temporary) | unknown
		}
		clean := path.Clean(suffix)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return (base &^ temporary) | unknown
		}
		return base
	}
	return unknown
}

func constantString(x expr) (string, bool) {
	if x.Kind == "literal" {
		return x.Text, true
	}
	if x.Kind == "concat" || x.Kind == "join" {
		var parts []string
		for _, a := range x.Args {
			s, ok := constantString(a)
			if !ok {
				return "", false
			}
			parts = append(parts, s)
		}
		if x.Kind == "join" {
			return path.Join(parts...), true
		}
		return strings.Join(parts, ""), true
	}
	return "", false
}

func (x expr) display() string {
	if s, ok := constantString(x); ok {
		return strconv.Quote(s)
	}
	switch x.Kind {
	case "temp":
		return "test-owned temporary path"
	case "embed":
		return "embedded filesystem"
	case "memory":
		return "in-memory filesystem"
	case "param":
		return fmt.Sprintf("parameter %d", x.Index)
	case "dirfs", "sub", "root", "handle":
		if len(x.Args) > 0 {
			return x.Kind + "(" + x.Args[0].display() + ")"
		}
	case "join", "concat", "union":
		var parts []string
		for _, a := range x.Args {
			parts = append(parts, a.display())
		}
		return x.Kind + "(" + strings.Join(parts, ", ") + ")"
	}
	return "unresolved value"
}

func bounded(x expr) bool {
	n := 0
	var visit func(expr, int) bool
	visit = func(x expr, depth int) bool {
		n++
		if depth > 12 || n > 128 {
			return false
		}
		for _, a := range x.Args {
			if !visit(a, depth+1) {
				return false
			}
		}
		return true
	}
	return visit(x, 0)
}
