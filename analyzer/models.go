package analyzer

import (
	"go/constant"
	"go/token"
	"os"

	"golang.org/x/tools/go/ssa"
)

// ModelVersion versions the standard-library API model independently of JSON.
const ModelVersion = 1

func (v *evaluator) model(c *ssa.CallCommon, args []expr, pos token.Pos, emit bool) (expr, bool) {
	pkg, name, method := identity(c)
	arg := func(i int) expr {
		if i >= 0 && i < len(args) {
			return args[i]
		}
		return leaf("unknown")
	}
	op := func(name string, value expr, write bool) {
		if emit {
			loc := v.e.location(pos)
			v.emit(operation{Name: name, Value: value, Write: write, Site: loc, Source: loc})
		}
	}
	result := func(x expr) expr {
		if c.Signature().Results().Len() > 1 {
			vals := make([]expr, c.Signature().Results().Len())
			vals[0] = x
			for i := 1; i < len(vals); i++ {
				vals[i] = leaf("unknown")
			}
			return node("tuple", vals...)
		}
		return x
	}
	if pkg == "testing" && method {
		if name == "TempDir" {
			return leaf("temp"), true
		}
		if name == "Run" || name == "Fuzz" || name == "Cleanup" {
			// testing invokes these callbacks. Analyze their body at its own source
			// locations, supplying captured values and unknown callback parameters.
			for _, actual := range c.Args {
				if iface, ok := actual.(*ssa.MakeInterface); ok {
					actual = iface.X
				}
				var fn *ssa.Function
				var bindings []ssa.Value
				switch x := actual.(type) {
				case *ssa.MakeClosure:
					fn, _ = x.Fn.(*ssa.Function)
					bindings = x.Bindings
				case *ssa.Function:
					fn = x
				}
				if fn == nil {
					continue
				}
				s, ok := v.e.summaries[fn]
				if !ok {
					continue
				}
				params := make([]expr, len(fn.Params))
				for i := range params {
					params[i] = leaf("unknown")
				}
				for _, b := range bindings {
					params = append(params, v.value(b))
				}
				if emit {
					for _, o := range s.Ops {
						o.Value = substitute(o.Value, params)
						v.emit(o)
					}
					v.result.Incomplete = v.result.Incomplete || s.Incomplete
					v.result.TaintsTemp = v.result.TaintsTemp || s.TaintsTemp
				}
			}
			return leaf("unknown"), true
		}
	}
	if pkg == "path/filepath" || pkg == "path" {
		switch name {
		case "Join":
			x := arg(0)
			if x.Kind == "struct" || x.Kind == "array" {
				return node("join", x.Args...), true
			}
			return leaf("unknown"), true
		case "Clean":
			return arg(0), true
		case "Glob", "Walk", "WalkDir":
			op("directory "+pkg+"."+name, arg(0), false)
			return leaf("unknown"), true
		}
	}
	if pkg == "os" && !method {
		switch name {
		case "TempDir":
			return leaf("systemtemp"), true
		case "MkdirTemp", "CreateTemp":
			x := node("tempcreate", arg(0), arg(1))
			op("write os."+name, x, true)
			if name == "CreateTemp" {
				x = node("handle", x)
			}
			return result(x), true
		case "DirFS":
			return node("dirfs", arg(0)), true
		case "OpenRoot":
			op("open os.OpenRoot", arg(0), false)
			return result(node("root", arg(0))), true
		case "ReadFile", "Open", "Stat", "Lstat", "ReadDir", "Readlink":
			kind := "read "
			if name == "Open" {
				kind = "open "
			}
			if name == "Stat" || name == "Lstat" {
				kind = "metadata "
			}
			if name == "ReadDir" {
				kind = "directory "
			}
			op(kind+"os."+name, arg(0), false)
			if name == "Open" {
				return result(node("handle", arg(0))), true
			}
			return leaf("unknown"), true
		case "OpenFile":
			write := writeFlags(c, 1)
			kind := "open "
			if write {
				kind = "write/open "
			}
			op(kind+"os.OpenFile", arg(0), write)
			return result(node("handle", arg(0))), true
		case "WriteFile", "Create", "Mkdir", "MkdirAll", "Remove", "RemoveAll", "Chmod", "Chtimes", "Truncate":
			op("write os."+name, arg(0), true)
			if name == "Create" {
				return result(node("handle", arg(0))), true
			}
			return leaf("unknown"), true
		case "Rename", "Link", "Symlink":
			if emit {
				v.result.TaintsTemp = true
			}
			op("write os."+name, arg(1), true)
			return leaf("unknown"), true
		case "Chdir":
			op("directory os.Chdir", arg(0), false)
			return leaf("unknown"), true
		}
	}
	if pkg == "os" && method {
		fn := c.StaticCallee()
		if fn != nil && named(fn.Signature.Recv().Type(), "os", "File") {
			if name == "Name" {
				x := arg(0)
				if x.Kind == "handle" {
					return x.Args[0], true
				}
				return leaf("unknown"), true
			}
			// The originating open is the finding, not every handle operation.
			return leaf("unknown"), true
		}
		if fn != nil && named(fn.Signature.Recv().Type(), "os", "Root") {
			base := arg(0)
			if base.Kind == "root" {
				base = base.Args[0]
			}
			if name == "FS" {
				return node("dirfs", base), true
			}
			if name == "Close" || name == "Name" {
				return leaf("unknown"), true
			}
			p := node("join", base, arg(1))
			switch name {
			case "Open", "OpenRoot", "ReadFile", "Stat", "Lstat", "Readlink":
				op("access os.Root."+name, p, false)
				x := node("handle", p)
				if name == "OpenRoot" {
					x = node("root", p)
				}
				return result(x), true
			case "OpenFile":
				write := writeFlags(c, 2)
				kind := "open "
				if write {
					kind = "write/open "
				}
				op(kind+"os.Root.OpenFile", p, write)
				return result(node("handle", p)), true
			case "WriteFile", "Create", "Mkdir", "MkdirAll", "Remove", "RemoveAll", "Chmod", "Chown", "Lchown", "Chtimes":
				op("write os.Root."+name, p, true)
				if name == "Create" {
					return result(node("handle", p)), true
				}
				return leaf("unknown"), true
			case "Symlink", "Rename", "Link":
				if emit {
					v.result.TaintsTemp = true
				}
				op("write os.Root."+name, node("join", base, arg(2)), true)
				return leaf("unknown"), true
			}
		}
	}
	if pkg == "io/fs" || pkg == "embed" || pkg == "testing/fstest" {
		receiver := arg(0)
		if c.IsInvoke() {
			receiver = v.value(c.Value)
		}
		if pkg == "embed" {
			receiver = leaf("embed")
		}
		if pkg == "testing/fstest" {
			receiver = leaf("memory")
		}
		switch name {
		case "Sub":
			return result(node("sub", receiver)), true
		case "ReadFile", "ReadDir", "Stat", "Glob", "WalkDir", "Open":
			op("access "+pkg+"."+name, receiver, false)
			if name == "Open" {
				return result(node("handle", receiver)), true
			}
			return leaf("unknown"), true
		}
	}
	if pkg == "text/template" || pkg == "html/template" {
		offset := 0
		if method {
			offset = 1
		}
		switch name {
		case "ParseFS":
			op("read "+pkg+".ParseFS", arg(offset), false)
			return leaf("unknown"), true
		case "ParseGlob":
			op("directory "+pkg+".ParseGlob", arg(offset), false)
			return leaf("unknown"), true
		case "ParseFiles":
			xs := arg(offset)
			if xs.Kind == "struct" {
				for _, x := range xs.Args {
					op("read "+pkg+".ParseFiles", x, false)
				}
			} else {
				op("read "+pkg+".ParseFiles", xs, false)
			}
			return leaf("unknown"), true
		}
	}
	return expr{}, false
}

func writeFlags(c *ssa.CallCommon, index int) bool {
	if index < len(c.Args) {
		if flags, ok := c.Args[index].(*ssa.Const); ok && flags.Value != nil {
			n, _ := constant.Int64Val(flags.Value)
			return n&int64(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
		}
	}
	return true
}
