package analyzer

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

type operation struct {
	Name   string
	Write  bool
	Value  expr
	Site   Location
	Source Location
}
type summary struct {
	Ops        []operation
	Returns    []expr
	Incomplete bool
	TaintsTemp bool
	Unresolved int
}
type functionFact struct {
	Version int
	Summary summary
}

func (*functionFact) AFact()           {}
func (f *functionFact) String() string { return fmt.Sprintf("testfs/v%d", f.Version) }

type evaluator struct {
	e        *engine
	fn       *ssa.Function
	memo     map[ssa.Value]expr
	visiting map[ssa.Value]bool
	result   summary
	seenOps  map[string]bool
}

func (e *engine) summarize(fn *ssa.Function) summary {
	v := &evaluator{e: e, fn: fn, memo: map[ssa.Value]expr{}, visiting: map[ssa.Value]bool{}, seenOps: map[string]bool{}}
	v.result.Returns = make([]expr, fn.Signature.Results().Len())
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			switch ins := ins.(type) {
			case ssa.CallInstruction:
				v.call(ins.Common(), ins.Pos(), true)
			case *ssa.Return:
				for i, x := range ins.Results {
					v.result.Returns[i] = union(v.result.Returns[i], v.value(x))
				}
			}
		}
	}
	for i, x := range v.result.Returns {
		if !bounded(x) {
			v.result.Returns[i] = leaf("unknown")
			v.result.Incomplete = true
		}
	}
	return v.result
}

func meaningful(xs []expr) bool {
	for _, x := range xs {
		if x.Kind != "" && x.Kind != "unknown" {
			return true
		}
	}
	return false
}

func (v *evaluator) value(x ssa.Value) expr {
	if x == nil {
		return leaf("unknown")
	}
	if val, ok := v.memo[x]; ok {
		return val
	}
	if v.visiting[x] {
		return expr{}
	}
	v.visiting[x] = true
	val := v.eval(x)
	delete(v.visiting, x)
	if !bounded(val) {
		val = leaf("unknown")
		v.result.Incomplete = true
	}
	v.memo[x] = val
	return val
}

func (v *evaluator) eval(x ssa.Value) expr {
	if named(x.Type(), "embed", "FS") {
		return leaf("embed")
	}
	if named(x.Type(), "testing/fstest", "MapFS") {
		return leaf("memory")
	}
	switch x := x.(type) {
	case *ssa.Const:
		if x.Value != nil && x.Value.Kind() == constant.String {
			return literal(constant.StringVal(x.Value))
		}
	case *ssa.Parameter:
		for i, p := range v.fn.Params {
			if x == p {
				return expr{Kind: "param", Index: i}
			}
		}
	case *ssa.FreeVar:
		for i, p := range v.fn.FreeVars {
			if x == p {
				return expr{Kind: "param", Index: len(v.fn.Params) + i}
			}
		}
	case *ssa.MakeInterface:
		value := v.value(x.X)
		if named(x.Type(), "io/fs", "FS") {
			return v.filesystem(x.X, value)
		}
		return value
	case *ssa.ChangeInterface:
		return v.value(x.X)
	case *ssa.ChangeType:
		return v.value(x.X)
	case *ssa.Convert:
		return v.value(x.X)
	case *ssa.TypeAssert:
		if x.CommaOk {
			return node("tuple", v.value(x.X), leaf("unknown"))
		}
		return v.value(x.X)
	case *ssa.Phi:
		var args []expr
		for _, a := range x.Edges {
			args = append(args, v.value(a))
		}
		return union(args...)
	case *ssa.Extract:
		return field(v.value(x.Tuple), x.Index)
	case *ssa.Call:
		return v.call(x.Common(), x.Pos(), false)
	case *ssa.BinOp:
		if x.Op == token.ADD {
			return node("concat", v.value(x.X), v.value(x.Y))
		}
	case *ssa.UnOp:
		if x.Op == token.MUL {
			return v.value(x.X)
		}
	case *ssa.Field:
		return field(v.value(x.X), x.Field)
	case *ssa.FieldAddr:
		return union(v.stores(x), field(v.value(x.X), x.Field))
	case *ssa.IndexAddr:
		return v.stores(x)
	case *ssa.Slice:
		return v.value(x.X)
	case *ssa.Alloc:
		stored := v.stores(x)
		if st, ok := x.Type().Underlying().(*types.Pointer); ok {
			var fields []expr
			switch t := st.Elem().Underlying().(type) {
			case *types.Struct:
				fields = make([]expr, t.NumFields())
			case *types.Array:
				if t.Len() <= 128 {
					fields = make([]expr, int(t.Len()))
				}
			}
			if fields != nil {
				if stored.Kind != "" {
					for i := range fields {
						fields[i] = field(stored, i)
					}
				}
				if refs := x.Referrers(); refs != nil {
					for _, r := range *refs {
						switch addr := r.(type) {
						case *ssa.FieldAddr:
							fields[addr.Field] = union(fields[addr.Field], v.stores(addr))
						case *ssa.IndexAddr:
							if c, ok := addr.Index.(*ssa.Const); ok && c.Value != nil {
								idx, _ := constant.Int64Val(c.Value)
								if idx >= 0 && idx < int64(len(fields)) {
									fields[idx] = union(fields[idx], v.stores(addr))
								}
							}
						}
					}
				}
				for i := range fields {
					if fields[i].Kind == "" {
						fields[i] = leaf("unknown")
					}
				}
				return node("struct", fields...)
			}
		}
		return stored
	case *ssa.Global:
		var vals []expr
		for _, val := range v.e.globals[x] {
			vals = append(vals, v.value(val))
		}
		if len(vals) > 0 {
			return union(vals...)
		}
	}
	return leaf("unknown")
}

func (v *evaluator) stores(x ssa.Value) expr {
	var vals []expr
	if refs := x.Referrers(); refs != nil {
		for _, r := range *refs {
			if st, ok := r.(*ssa.Store); ok && st.Addr == x {
				vals = append(vals, v.value(st.Val))
			}
		}
	}
	return union(vals...)
}

// Summarize a concrete FS wrapper from its Open implementation. Looking only
// at the fields would wrongly allow a wrapper that ignores its embedded FS and
// opens disk files instead. No user-maintained list of wrapper names is needed.
func (v *evaluator) filesystem(actual ssa.Value, value expr) expr {
	if named(actual.Type(), "embed", "FS") || named(actual.Type(), "testing/fstest", "MapFS") {
		return value
	}
	fn := v.e.pkg.Prog.LookupMethod(actual.Type(), nil, "Open")
	if fn == nil {
		return value
	}
	s, found := v.e.summaries[fn]
	if !found && fn.Object() != nil {
		var fact functionFact
		if v.e.pass.ImportObjectFact(fn.Object(), &fact) && fact.Version == 1 {
			s = fact.Summary
			found = true
		}
	}
	if !found {
		return leaf("unknown")
	}
	args := []expr{value, leaf("unknown")}
	var origins []expr
	for _, op := range s.Ops {
		origins = append(origins, substitute(op.Value, args))
	}
	if len(s.Returns) > 0 {
		origins = append(origins, substitute(s.Returns[0], args))
	}
	if len(origins) == 0 {
		return leaf("unknown")
	}
	return union(origins...)
}

func (v *evaluator) emit(op operation) {
	if !bounded(op.Value) {
		op.Value = leaf("unknown")
		v.result.Incomplete = true
	}
	key := fmt.Sprintf("%v/%s/%s/%v", op.Source, op.Name, op.Value.key(), op.Site)
	if v.seenOps[key] {
		return
	}
	v.seenOps[key] = true
	if len(v.result.Ops) >= 128 {
		v.result.Incomplete = true
		return
	}
	v.result.Ops = append(v.result.Ops, op)
}

func (v *evaluator) call(c *ssa.CallCommon, pos token.Pos, emit bool) expr {
	args := make([]expr, len(c.Args))
	for i, a := range c.Args {
		args[i] = v.value(a)
	}
	if val, ok := v.model(c, args, pos, emit); ok {
		return val
	}
	fn := c.StaticCallee()
	if closure, ok := c.Value.(*ssa.MakeClosure); ok {
		for _, b := range closure.Bindings {
			args = append(args, v.value(b))
		}
	}
	if fn == nil && c.IsInvoke() {
		if iface, ok := c.Value.(*ssa.MakeInterface); ok {
			fn = v.e.pkg.Prog.LookupMethod(iface.X.Type(), c.Method.Pkg(), c.Method.Name())
			args = append([]expr{v.value(iface.X)}, args...)
		}
	}
	var s summary
	found := false
	if fn != nil {
		s, found = v.e.summaries[fn]
		if !found && fn.Object() != nil {
			var fact functionFact
			if v.e.pass.ImportObjectFact(fn.Object(), &fact) && fact.Version == 1 {
				s = fact.Summary
				found = true
			}
		}
	}
	if !found {
		if emit && pos.IsValid() {
			if _, builtin := c.Value.(*ssa.Builtin); !builtin {
				v.result.Unresolved++
			}
		}
		return leaf("unknown")
	}
	if emit {
		v.result.Incomplete = v.result.Incomplete || s.Incomplete
		v.result.TaintsTemp = v.result.TaintsTemp || s.TaintsTemp
		// Count unresolved call sites without recursively multiplying cycles.
		if s.Unresolved > 0 {
			v.result.Unresolved++
		}
		for _, op := range s.Ops {
			op.Value = substitute(op.Value, args)
			op.Site = v.e.location(pos)
			v.emit(op)
		}
	}
	returns := make([]expr, len(s.Returns))
	for i, r := range s.Returns {
		returns[i] = substitute(r, args)
	}
	if len(returns) == 1 {
		return returns[0]
	}
	return node("tuple", returns...)
}

func identity(c *ssa.CallCommon) (pkg, name string, method bool) {
	var obj *types.Func
	if c.IsInvoke() {
		obj = c.Method
		method = true
	} else if fn := c.StaticCallee(); fn != nil {
		obj, _ = fn.Object().(*types.Func)
		method = fn.Signature.Recv() != nil
	}
	if obj == nil || obj.Pkg() == nil {
		return "", "", false
	}
	return obj.Pkg().Path(), obj.Name(), method
}
