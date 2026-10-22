package sema

// interp.go interprets the bodies of pure functions (and the procedures
// they call) for static evaluation (#34): the bundled ieee packages and
// user functions run from their source, statement by statement, under
// the caller's step budget (P7). The interpreter only reads the typed
// trees (SeqStmt, Expr); every expression is lowered and evaluated by
// internal/eval, the evaluator the simulator shares (#35).
//
// A call runs in a frame: the parameters, variables, constants and
// aliases of the body are cells of the frame. Lowering a name of a cell
// reads its current value, so each expression is lowered right before
// it is evaluated. Type layouts and constant values computed in a frame
// without reading a cell do not depend on the call and are cached for
// the whole evaluator; the others are cached for the frame only.

import (
	"slices"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/eval"
)

// maxCallDepth bounds the nesting of interpreted calls. A recursion
// deeper than this is reported like the step limit: the value could
// not be computed (P7).
const maxCallDepth = 2048

// shared is the state every evaluator derived from one NewEvaluator
// shares: the subprogram body index, the call depth, and the layouts
// and constant values that depend on no binding and no cell, so every
// instance sees one layout per type (values of different instances
// share their base types).
type shared struct {
	bodies  map[*Unit]map[*Decl]*Decl // per unit: spec -> body
	depth   int
	layouts map[*Type]*eval.Type
	consts  map[*Decl]constResult
	// reads counts the reads of bindings (and of the caches of values
	// that depend on them) and the objects left to Env.Load: a
	// computation during which it does not change is the same in every
	// instance. Frame cells are counted by frame.loads.
	reads int
	// dynReads counts the reads of a simulator context (dynamic.go):
	// what reads it is never cached.
	dynReads int
	// memo records pure function calls (memo.go); lookup only. fills
	// counts the layouts and constants computed: a call that computes
	// one costs more than its repetitions and is not recorded.
	memo  map[string]memoEntry
	fills int
	// natives evaluates the calls of functions without a body during
	// elaboration (natives.go); nil leaves them not static.
	natives Natives
	// instSubs are the actuals of the generic subprograms of each
	// subprogram instance, by generic position (instanceSubs).
	instSubs map[*Decl][]*Decl
}

func newShared() *shared {
	return &shared{bodies: map[*Unit]map[*Decl]*Decl{},
		layouts: map[*Type]*eval.Type{}, consts: map[*Decl]constResult{}}
}

// frame is one interpreted call.
type frame struct {
	vars    map[*Decl]*cell
	consts  map[*Decl]constResult
	layouts map[*Type]*eval.Type
	// loads counts the reads of cells, for the dependency tracking of
	// the caches.
	loads int
}

// cell is one object of a frame.
type cell struct {
	v eval.Value
	// t is the object's subtype (scalar range checks on assignment).
	t *eval.Type
	// owned reports that v.E belongs to the cell alone, so an element
	// assignment may write it in place.
	owned bool
	// alias is the object an object alias denotes: assignments through
	// the alias are written back to it.
	alias *Expr
}

// flow is how a statement sequence ended.
type flow uint8

const (
	flowNormal flow = iota
	flowNext
	flowExit
	flowReturn
)

type outcome struct {
	flow flow
	loop *Decl // the loop of next/exit (nil: the innermost)
	v    eval.Value
}

// Call implements eval.Env: it interprets the body of the pure function
// fn on args. Impure functions, functions without a body (the kernel's
// std packages) and calls deeper than maxCallDepth are not static.
func (ev *Evaluator) Call(fn eval.Func, args []eval.Value, b *eval.Budget,
) (eval.Value, error) {
	if pr, ok := b.Observer().(*Profile); ok { // profile.go
		if d, ok := fn.(*Decl); ok {
			pr.enter(d.Denotes())
			defer pr.exit()
		}
	}
	if ev.dyn != nil {
		if v, ok, err := ev.dyn.Call(fn, args, b); ok {
			ev.sh.dynReads++
			return v, err
		}
	}
	if n, ok := fn.(*nativeCall); ok {
		return ev.callNative(n, args, b)
	}
	d, ok := fn.(*Decl)
	if !ok {
		return eval.Value{}, eval.NotStatic("call")
	}
	x := d.Denotes()
	if x.Kind != DeclFunction || !Pure(x) {
		return eval.Value{}, eval.NotStatic("call of " + x.Text)
	}
	var in *Instance
	if InstanceOf(x) != nil {
		// The generics are bound in the caller's context, before the
		// call (its memo key includes them).
		var err error
		if in, err = ev.instance(x, b); err != nil {
			return eval.Value{}, err
		}
	}
	if ev.memo {
		return ev.memoCall(x, in, args, b) // memo.go
	}
	return ev.pureCall(x, in, args, b)
}

// pureCall interprets the body of the pure function x on args; in
// binds the generics of an instance.
func (ev *Evaluator) pureCall(x *Decl, in *Instance, args []eval.Value,
	b *eval.Budget,
) (eval.Value, error) {
	out, err := ev.invoke(x, in, args, nil, b)
	if err != nil {
		return eval.Value{}, err
	}
	if out.flow != flowReturn || out.v.T == nil {
		return eval.Value{}, eval.NotStatic("function " + x.Text +
			" ended without return")
	}
	return out.v, nil
}

// invoke runs the body of the subprogram x with the parameter values
// args (in parameter order). For a procedure, outs receives the final
// values of the parameters. in binds the generics of an instance x
// (nil: they are bound here).
func (ev *Evaluator) invoke(x *Decl, in *Instance, args []eval.Value,
	outs []eval.Value, b *eval.Budget,
) (outcome, error) {
	if in == nil && InstanceOf(x) != nil {
		var err error
		if in, err = ev.instance(x, b); err != nil {
			return outcome{}, err
		}
	}
	body := ev.body(x)
	if body == nil || body.Region == nil {
		return outcome{}, eval.NotStatic("call of " + x.Text)
	}
	if len(body.Params) != len(args) {
		return outcome{}, eval.NotStatic("call of " + x.Text)
	}
	if ev.sh.depth >= maxCallDepth {
		return outcome{}, eval.ErrBudget
	}
	if err := b.Step(1); err != nil {
		return outcome{}, err
	}
	ev.sh.depth++
	defer func() { ev.sh.depth-- }()
	if pr, ok := b.Observer().(*Profile); ok { // profile.go
		pr.enter(x)
		defer pr.exit()
	}
	fe := ev.enter()
	if in != nil {
		fe = in.fe
	}
	for i, p := range body.Params {
		if p.Class == ClassSignal || p.Class == ClassFile {
			return outcome{}, eval.NotStatic(p.Class.word() +
				" parameter " + p.Text)
		}
		pt, err := fe.layout(p.Type, b)
		if err != nil {
			return outcome{}, err
		}
		v, err := eval.Check(args[i], pt)
		if err != nil {
			return outcome{}, err
		}
		fe.fr.vars[p] = &cell{v: v, t: pt}
	}
	if err := fe.declare(body.Region, b); err != nil {
		return outcome{}, err
	}
	out, err := fe.exec(body.Seq, b)
	if err != nil {
		return outcome{}, err
	}
	for i, p := range body.Params {
		if i < len(outs) {
			outs[i] = fe.fr.vars[p].v
		}
	}
	return out, nil
}

func (c ObjClass) word() string {
	switch c {
	case ClassSignal:
		return "signal"
	case ClassFile:
		return "file"
	case ClassVariable:
		return "variable"
	}
	return "constant"
}

// enter returns an evaluator for a new frame: the bindings and caches
// of ev's instance, no cells.
func (ev *Evaluator) enter() *Evaluator {
	c := *ev
	// consts and layouts are made on first use.
	c.fr = &frame{vars: make(map[*Decl]*cell, 8)}
	return &c
}

// loads returns the frame's cell-read count (0 outside a frame).
func (ev *Evaluator) loads() int {
	if ev.fr == nil {
		return 0
	}
	return ev.fr.loads
}

// body returns the body of the subprogram x: x itself when it is a
// body, else the body in x's unit or, for a package subprogram, in the
// package body.
func (ev *Evaluator) body(x *Decl) *Decl {
	if g := InstanceOf(x); g != nil {
		// An instance runs the uninstantiated subprogram's body.
		return ev.body(g)
	}
	if x.Node.Kind() == cst.KindSubprogramBody {
		return x
	}
	if x.Unit == nil {
		return nil
	}
	if b := ev.bodyIn(x.Unit, x); b != nil {
		return b
	}
	if x.Unit.Kind != UnitPackage || ev.libs == nil {
		return nil
	}
	l, ok := ev.libs(x.Unit.Library)
	if !ok {
		return nil
	}
	for _, u := range l.Secondary(x.Unit.Name) {
		if u.Kind == UnitPackageBody {
			if b := ev.bodyIn(u, x); b != nil {
				return b
			}
		}
	}
	return nil
}

func (ev *Evaluator) bodyIn(u *Unit, x *Decl) *Decl {
	idx, ok := ev.sh.bodies[u]
	if !ok {
		idx = map[*Decl]*Decl{}
		for _, d := range u.Decls {
			if d.Kind.IsSubprogram() && d.Spec != nil &&
				d.Node.Kind() == cst.KindSubprogramBody {
				idx[d.Spec] = d
			}
		}
		ev.sh.bodies[u] = idx
	}
	return idx[x]
}

// declare elaborates the declarations of a subprogram body or
// sequential block region in order: variables, constants and object
// aliases become cells. Types are laid out when used.
func (ev *Evaluator) declare(r *Region, b *eval.Budget) error {
	for _, d := range r.Decls {
		switch d.Kind {
		case DeclVariable, DeclConstant:
			t, err := ev.layout(d.Type, b)
			if err != nil {
				return err
			}
			var v eval.Value
			if d.Value != nil {
				if v, err = ev.value(d.Value, b); err == nil {
					// An open constraint comes from the value
					// (VHDL-2019 inferred constraints, LRM 6.4.2.1).
					t = eval.InferSubtype(t, v)
					v, err = eval.Check(v, t)
				}
			} else {
				v, err = eval.Default(b, t)
			}
			if err != nil {
				return err
			}
			ev.fr.vars[d] = &cell{v: v, t: t}
		case DeclAlias:
			if d.Value == nil {
				continue // a non-object alias
			}
			v, err := ev.value(d.Value, b)
			if err != nil {
				return err
			}
			t := v.T
			if d.Type != nil && d.Type != d.Value.Type {
				if t, err = ev.layout(d.Type, b); err != nil {
					return err
				}
				if v, err = eval.Check(v, t); err != nil {
					return err
				}
			}
			ev.fr.vars[d] = &cell{v: v, t: t, alias: d.Value}
		case DeclFile, DeclSharedVariable, DeclSignal:
			return eval.NotStatic(d.Kind.String() + " " + d.Text)
		}
	}
	return nil
}

// exec runs a statement sequence.
func (ev *Evaluator) exec(ss []*SeqStmt, b *eval.Budget) (outcome, error) {
	for _, s := range ss {
		if err := b.Step(1); err != nil {
			return outcome{}, err
		}
		out, err := ev.stmt(s, b)
		if err != nil || out.flow != flowNormal {
			return out, err
		}
	}
	return outcome{}, nil
}

func (ev *Evaluator) stmt(s *SeqStmt, b *eval.Budget) (outcome, error) {
	switch s.Kind {
	case SeqNull, SeqAssert, SeqReport:
		// Assertions do not change values; the simulator reports them.
		return outcome{}, nil
	case SeqVarAssign:
		v, err := ev.value(s.Value, b)
		if err != nil {
			return outcome{}, err
		}
		return outcome{}, ev.assign(s.Target, v, b)
	case SeqCondVarAssign:
		for _, br := range s.Branches {
			ok, err := ev.cond(br.Cond, b)
			if err != nil {
				return outcome{}, err
			}
			if ok {
				return outcome{}, ev.assignValue(s.Target, br.Value, b)
			}
		}
		return outcome{}, nil
	case SeqSelVarAssign:
		i, err := ev.choose(s.Selector, s.Alts, s.Matching, b)
		if err != nil || i < 0 {
			return outcome{}, err
		}
		return outcome{}, ev.assignValue(s.Target, s.Alts[i].Value, b)
	case SeqCall:
		return outcome{}, ev.procedure(s.Call, b)
	case SeqIf:
		for _, br := range s.Branches {
			ok, err := ev.cond(br.Cond, b)
			if err != nil {
				return outcome{}, err
			}
			if ok {
				return ev.exec(br.Body, b)
			}
		}
		return ev.exec(s.Else, b)
	case SeqCase:
		i, err := ev.choose(s.Selector, s.Alts, s.Matching, b)
		if err != nil || i < 0 {
			return outcome{}, err
		}
		return ev.exec(s.Alts[i].Body, b)
	case SeqLoop:
		return ev.loop(s, b)
	case SeqNext, SeqExit:
		if s.Cond != nil {
			ok, err := ev.cond(s.Cond, b)
			if err != nil || !ok {
				return outcome{}, err
			}
		}
		f := flowNext
		if s.Kind == SeqExit {
			f = flowExit
		}
		return outcome{flow: f, loop: s.Loop}, nil
	case SeqReturn:
		out := outcome{flow: flowReturn}
		if s.Value != nil {
			v, err := ev.value(s.Value, b)
			if err != nil {
				return outcome{}, err
			}
			out.v = v
		}
		return out, nil
	case SeqCondReturn:
		for _, br := range s.Branches {
			ok, err := ev.cond(br.Cond, b)
			if err != nil {
				return outcome{}, err
			}
			if !ok {
				continue
			}
			// LRM 10.13: an unaffected branch returns nothing and
			// the statement does nothing; a branch with no value is
			// a procedure's return.
			if br.Unaffected {
				return outcome{}, nil
			}
			out := outcome{flow: flowReturn}
			if br.Value != nil {
				if out.v, err = ev.value(br.Value, b); err != nil {
					return outcome{}, err
				}
			}
			return out, nil
		}
		return outcome{}, nil
	case SeqBlock:
		if s.Region != nil {
			if err := ev.declare(s.Region, b); err != nil {
				return outcome{}, err
			}
		}
		return ev.exec(s.Body, b)
	}
	return outcome{}, eval.NotStatic("statement")
}

// cond evaluates a condition; a nil condition (an else) is true. A
// condition of another type than BOOLEAN is the VHDL-2008 "??" of BIT
// or STD_ULOGIC: '1' and 'H' are true.
func (ev *Evaluator) cond(e *Expr, b *eval.Budget) (bool, error) {
	if e == nil {
		return true, nil
	}
	v, err := ev.value(e, b)
	if err != nil {
		return false, err
	}
	return truth(v), nil
}

func truth(v eval.Value) bool {
	if v.Kind() != eval.KindEnum {
		return false
	}
	lits := v.T.BaseType().Literals
	if len(lits) == 2 && lits[0] == "false" {
		return v.I != 0
	}
	if v.I >= 0 && v.I < int64(len(lits)) {
		l := lits[v.I]
		return l == "'1'" || l == "'H'" || l == "true"
	}
	return false
}

// choose returns the alternative of a case statement or selected
// assignment that the selector's value picks (-1: none).
func (ev *Evaluator) choose(sel *Expr, alts []SeqAlt, matching bool,
	b *eval.Budget,
) (int, error) {
	v, err := ev.value(sel, b)
	if err != nil {
		return -1, err
	}
	for i, alt := range alts {
		if alt.Others {
			return i, nil
		}
		for _, c := range alt.Choices {
			if err := b.Step(1); err != nil {
				return -1, err
			}
			hit, err := ev.choiceHit(c, v, matching, b)
			if err != nil {
				return -1, err
			}
			if hit {
				return i, nil
			}
		}
	}
	return -1, nil
}

func (ev *Evaluator) choiceHit(c *Expr, v eval.Value, matching bool,
	b *eval.Budget,
) (bool, error) {
	switch c.Kind {
	case ExprRange:
		r, err := ev.rangeOf(c.Range, b)
		return err == nil && r.Contains(v.I), err
	case ExprTypeMark:
		t, err := ev.layout(c.Type, b)
		return err == nil && t.Range.Contains(v.I), err
	}
	w, err := ev.value(c, b)
	if err != nil {
		return false, err
	}
	if matching {
		return matches(v, w), nil
	}
	return v.Equal(w), nil
}

// matches is the choice match of case? (LRM 10.9): elements equal, or
// the choice element is '-'.
func matches(v, w eval.Value) bool {
	if v.Kind() != eval.KindArray {
		return v.Equal(w) || dontCare(w)
	}
	if len(v.E) != len(w.E) {
		return false
	}
	for i := range v.E {
		if !v.E[i].Equal(w.E[i]) && !dontCare(w.E[i]) {
			return false
		}
	}
	return true
}

func dontCare(v eval.Value) bool {
	if v.Kind() != eval.KindEnum {
		return false
	}
	lits := v.T.BaseType().Literals
	return v.I >= 0 && v.I < int64(len(lits)) && lits[v.I] == "'-'"
}

// loop runs a loop statement.
func (ev *Evaluator) loop(s *SeqStmt, b *eval.Budget) (outcome, error) {
	mine := func(o outcome) bool { return o.loop == nil || o.loop == s.Label }
	body := func() (bool, outcome, error) {
		out, err := ev.exec(s.Body, b)
		if err != nil {
			return true, out, err
		}
		switch out.flow {
		case flowNext:
			if mine(out) {
				return false, outcome{}, nil
			}
			return true, out, nil
		case flowExit:
			if mine(out) {
				return true, outcome{}, nil
			}
			return true, out, nil
		case flowReturn:
			return true, out, nil
		}
		return false, out, nil
	}
	if s.Param != nil {
		r, err := ev.rangeOf(s.Range, b)
		if err != nil {
			return outcome{}, err
		}
		pt, err := ev.layout(s.Param.Type, b)
		if err != nil {
			return outcome{}, err
		}
		c := &cell{t: pt}
		ev.fr.vars[s.Param] = c
		defer delete(ev.fr.vars, s.Param)
		for i := range r.Len() {
			if err := b.Step(1); err != nil {
				return outcome{}, err
			}
			c.v = eval.Value{T: pt, I: r.At(i)}
			if stop, out, err := body(); stop {
				return out, err
			}
		}
		return outcome{}, nil
	}
	for {
		if err := b.Step(1); err != nil {
			return outcome{}, err
		}
		if s.Cond != nil {
			ok, err := ev.cond(s.Cond, b)
			if err != nil || !ok {
				return outcome{}, err
			}
		}
		if stop, out, err := body(); stop {
			return out, err
		}
	}
}

// assignValue assigns the value of e (nil: unaffected) to target.
func (ev *Evaluator) assignValue(target, e *Expr, b *eval.Budget) error {
	if e == nil {
		return nil
	}
	v, err := ev.value(e, b)
	if err != nil {
		return err
	}
	return ev.assign(target, v, b)
}

// step is one selection on the way from a cell to an assigned element.
type step struct {
	slice bool
	field bool
	off   int64 // element offset, or the first element of a slice
	n     int64 // elements of a slice
}

// assign writes v to the variable target names: a cell, an element, a
// slice or a record field of one.
func (ev *Evaluator) assign(target *Expr, v eval.Value, b *eval.Budget,
) error {
	if ev.fr == nil {
		return eval.NotStatic("assignment")
	}
	c, path, err := ev.place(target, b)
	if err != nil {
		return err
	}
	if err := b.Step(1 + int64(len(v.E))); err != nil {
		return err
	}
	if len(path) == 0 {
		nv, err := assignWhole(c.v, v, c.t)
		if err != nil {
			return err
		}
		c.v, c.owned = nv, false
	} else {
		if !c.owned {
			c.v.E = append([]eval.Value(nil), c.v.E...)
			c.owned = true
		}
		nv, err := setAt(c.v, path, v, true)
		if err != nil {
			return err
		}
		c.v = nv
	}
	if c.alias != nil {
		return ev.assign(c.alias, c.v, b)
	}
	return nil
}

// place resolves an assignment target to its cell and the path to the
// element written.
func (ev *Evaluator) place(e *Expr, b *eval.Budget) (*cell, []step, error) {
	switch e.Kind {
	case ExprName:
		if e.Decl != nil {
			if c, ok := ev.fr.vars[e.Decl]; ok {
				return c, nil, nil
			}
			if c, ok := ev.fr.vars[e.Decl.Denotes()]; ok {
				return c, nil, nil
			}
		}
	case ExprIndex, ExprSlice, ExprSelect:
		c, path, err := ev.place(e.Prefix, b)
		if err != nil {
			return nil, nil, err
		}
		cur, err := getAt(c.v, path)
		if err != nil {
			return nil, nil, err
		}
		s, err := ev.select1(e, cur, b)
		if err != nil {
			return nil, nil, err
		}
		return c, append(path, s), nil
	}
	return nil, nil, eval.NotStatic("assignment target")
}

// select1 computes the step of an indexed name, slice or selection
// applied to the value cur.
func (ev *Evaluator) select1(e *Expr, cur eval.Value, b *eval.Budget,
) (step, error) {
	switch e.Kind {
	case ExprSelect:
		if cur.Kind() != eval.KindRecord || e.Decl == nil ||
			e.Decl.Pos >= len(cur.E) {
			return step{}, eval.NotStatic("selected target")
		}
		return step{field: true, off: int64(e.Decl.Pos)}, nil
	case ExprIndex:
		if cur.Kind() != eval.KindArray {
			return step{}, eval.NotStatic("indexed target")
		}
		off := int64(0)
		for d, ie := range e.Args {
			iv, err := ev.value(ie, b)
			if err != nil {
				return step{}, err
			}
			r := dimOf(cur, d)
			if !r.Contains(iv.I) {
				return step{}, eval.IndexError(iv.I, r)
			}
			off = off*r.Len() + r.Offset(iv.I)
		}
		if len(e.Args) < len(cur.B) {
			return step{}, eval.NotStatic("indexed target")
		}
		return step{off: off}, nil
	}
	// A slice.
	r, err := ev.rangeOf(e.Range, b)
	if err != nil {
		return step{}, err
	}
	ar := dimOf(cur, 0)
	if r.Null() {
		return step{slice: true}, nil
	}
	if r.Dir != ar.Dir || !ar.Contains(r.Left) || !ar.Contains(r.Right) {
		return step{}, eval.SliceError(r, ar)
	}
	return step{slice: true, off: ar.Offset(r.Left), n: r.Len()}, nil
}

func dimOf(a eval.Value, d int) eval.Range {
	if d < len(a.B) {
		return a.B[d]
	}
	return eval.Range{Left: 0, Right: int64(len(a.E)) - 1}
}

// getAt returns the element of v that path selects.
func getAt(v eval.Value, path []step) (eval.Value, error) {
	for _, s := range path {
		if s.slice || s.off < 0 || s.off >= int64(len(v.E)) {
			return eval.Value{}, eval.NotStatic("slice target")
		}
		v = v.E[s.off]
	}
	return v, nil
}

// setAt returns cur with the element path selects replaced by v. The
// elements of cur are copied unless owned.
func setAt(cur eval.Value, path []step, v eval.Value, owned bool,
) (eval.Value, error) {
	s := path[0]
	e := cur.E
	if !owned {
		e = append([]eval.Value(nil), e...)
	}
	if s.slice {
		// The value's elements replace the slice's.
		if len(path) > 1 {
			return eval.Value{}, eval.NotStatic("slice target")
		}
		if int64(len(v.E)) != s.n || s.off+s.n > int64(len(e)) {
			return eval.Value{}, eval.LengthError(s.n, int64(len(v.E)))
		}
		copy(e[s.off:], v.E)
		cur.E = e
		return cur, nil
	}
	if s.off < 0 || s.off >= int64(len(e)) {
		return eval.Value{}, eval.NotStatic("target element")
	}
	var err error
	if len(path) == 1 {
		e[s.off], err = assignWhole(e[s.off], v, e[s.off].T)
	} else {
		e[s.off], err = setAt(e[s.off], path[1:], v, false)
	}
	if err != nil {
		return eval.Value{}, err
	}
	cur.E = e
	return cur, nil
}

// assignWhole is the value an object holding old has after the
// assignment of v (LRM 10.6.2.1): scalars are checked against the
// object's subtype t, arrays must match the object's length and keep
// its bounds.
func assignWhole(old, v eval.Value, t *eval.Type) (eval.Value, error) {
	switch old.Kind() {
	case eval.KindArray:
		if v.Kind() != eval.KindArray {
			return eval.Value{}, eval.NotStatic("array assignment")
		}
		if len(old.B) > 1 {
			for d := range old.B {
				if d >= len(v.B) || v.B[d].Len() != old.B[d].Len() {
					return eval.Value{}, eval.LengthError(old.B[d].Len(),
						dimOf(v, d).Len())
				}
			}
		} else if len(v.E) != len(old.E) {
			return eval.Value{}, eval.LengthError(int64(len(old.E)),
				int64(len(v.E)))
		}
		es, err := assignElems(old.E, v.E)
		if err != nil {
			return eval.Value{}, err
		}
		return eval.Value{T: old.T, B: old.B, E: es}, nil
	case eval.KindRecord:
		if len(v.E) != len(old.E) {
			return eval.Value{}, eval.NotStatic("record assignment")
		}
		es, err := assignElems(old.E, v.E)
		if err != nil {
			return eval.Value{}, err
		}
		return eval.Value{T: old.T, E: es}, nil
	case eval.KindInvalid:
		return v, nil
	}
	if t == nil {
		t = old.T
	}
	return eval.Check(v, t)
}

// assignElems assigns the elements or fields vs over olds: a composite
// one keeps its own constraints, so a value of another length does not
// replace an element or field whose subtype was inferred (LRM 6.4.2.1)
// or constrained. Scalars are taken as they are.
func assignElems(olds, vs []eval.Value) ([]eval.Value, error) {
	if len(olds) != len(vs) {
		return vs, nil
	}
	var out []eval.Value
	for i, o := range olds {
		if k := o.Kind(); k != eval.KindArray && k != eval.KindRecord {
			continue
		}
		nv, err := assignWhole(o, vs[i], nil)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = slices.Clone(vs)
		}
		out[i] = nv
	}
	if out == nil {
		return vs, nil
	}
	return out, nil
}

// procedure interprets a procedure call statement: in parameters are
// evaluated, out and inout parameters are variables of the caller,
// written back when the procedure returns.
func (ev *Evaluator) procedure(e *Expr, b *eval.Budget) error {
	if e == nil || e.Decl == nil {
		return eval.NotStatic("procedure call")
	}
	e, err := ev.Dispatch(e) // a generic procedure: its actual
	if err != nil {
		return err
	}
	x := e.Decl.Denotes()
	if x.Kind != DeclProcedure {
		return eval.NotStatic("procedure call")
	}
	acts, err := actuals(e, x)
	if err != nil {
		return err
	}
	args := make([]eval.Value, len(acts))
	for i, a := range acts {
		p := x.Params[i]
		if p.Mode == ModeOut || p.Mode == ModeInout {
			if p.Class == ClassSignal || ev.fr == nil {
				return eval.NotStatic("procedure call")
			}
			c, path, err := ev.place(a, b)
			if err != nil {
				return err
			}
			if args[i], err = getAtSlice(c.v, path); err != nil {
				return err
			}
			continue
		}
		if args[i], err = ev.value(a, b); err != nil {
			return err
		}
	}
	outs := make([]eval.Value, len(args))
	if _, err := ev.invoke(x, nil, args, outs, b); err != nil {
		return err
	}
	for i, a := range acts {
		p := x.Params[i]
		if p.Mode == ModeOut || p.Mode == ModeInout {
			if err := ev.assign(a, outs[i], b); err != nil {
				return err
			}
		}
	}
	return nil
}

// getAtSlice is getAt for the current value of an out actual, which
// may be a slice.
func getAtSlice(v eval.Value, path []step) (eval.Value, error) {
	for i, s := range path {
		if s.slice && i == len(path)-1 && s.n > 0 {
			out := v
			out.E = v.E[s.off : s.off+s.n]
			r := dimOf(v, 0)
			out.B = []eval.Range{{Left: r.At(s.off),
				Right: r.At(s.off + s.n - 1), Dir: r.Dir}}
			return out, nil
		}
		if s.slice || s.off < 0 || s.off >= int64(len(v.E)) {
			return eval.Value{}, eval.NotStatic("actual")
		}
		v = v.E[s.off]
	}
	return v, nil
}

// actuals returns the actual of each parameter of x in a call e, in
// parameter order: the associated expression or the default value.
func actuals(e *Expr, x *Decl) ([]*Expr, error) {
	out := make([]*Expr, len(x.Params))
	for i, a := range e.Args {
		var f *Decl
		if i < len(e.Formals) {
			f = e.Formals[i]
		}
		if f == nil {
			if i >= len(out) || len(e.Formals) > 0 {
				return nil, eval.NotStatic("call of " + x.Text)
			}
			f = x.Params[i]
		}
		if f.Pos < 0 || f.Pos >= len(out) || x.Params[f.Pos] != f ||
			out[f.Pos] != nil || a == nil || a.Kind == ExprOpen {
			return nil, eval.NotStatic("call of " + x.Text)
		}
		out[f.Pos] = a
	}
	for i, p := range x.Params {
		if out[i] == nil {
			if p.Value == nil {
				return nil, eval.NotStatic("call of " + x.Text)
			}
			out[i] = p.Value
		}
	}
	return out, nil
}

// Subprogram instances (LRM 4.4, 9.3.4): a VHDL-2008 subprogram
// instantiation declaration and the implicit instance of a VHDL-2019
// call with a generic map run the body of the uninstantiated
// subprogram, with its generics bound for the call. The bindings are
// made by position: the n-th generic of the body is the n-th of the
// declaration the associations name. Generic constants and types are
// evaluated in the caller's context when the call is made (the actuals
// of a call's generic map may read the caller's variables); generic
// subprograms are resolved once per instance.

// InstanceOf returns the uninstantiated subprogram x is an instance
// of, nil when x is no subprogram instance.
func InstanceOf(x *Decl) *Decl {
	if x == nil || !x.Kind.IsSubprogram() || x.Generics == nil ||
		x.Target == nil || x.Target == x {
		return nil
	}
	g := x.Target.Denotes()
	if g == nil || g == x || !g.Kind.IsSubprogram() {
		return nil
	}
	return g
}

// Pure reports whether the function x is pure: an instance is as pure
// as the subprogram it instantiates (LRM 4.4).
func Pure(x *Decl) bool {
	if g := InstanceOf(x); g != nil {
		return g.Pure
	}
	return x.Pure
}

// genericsOf returns the generics of a subprogram's region in order.
func genericsOf(r *Region) []*Decl {
	if r == nil {
		return nil
	}
	var out []*Decl
	for _, d := range r.Decls {
		if isGenericKind(d.Kind) {
			out = append(out, d)
		}
	}
	return out
}

// cGenericPackageFormal names the construct of a subprogram instance
// with a generic package formal: the generics of package instances are
// not bound in simulation (their names resolve to the uninstantiated
// package's declarations), so neither is such a formal.
const cGenericPackageFormal = "generic package formals of subprograms"

// cPackageInstanceGeneric names the construct of a read of a generic
// constant of a package at run time: the names of a package instance
// resolve to the uninstantiated package's declarations, whose generics
// no simulation binds (#18: unsupported/construct, please report).
const cPackageInstanceGeneric = "generics of package instances read " +
	"at run time"

// packageGeneric reports whether x is a generic constant of a package
// (bound only in an instance of it).
func packageGeneric(x *Decl) bool {
	return x.Kind == DeclGeneric && x.Parent != nil &&
		x.Parent.Kind == RegionPackage
}

// UnsupportedError is the evaluation error of a legal construct GoHard
// does not simulate yet: the simulator reports What as
// unsupported/construct (please report). It unwraps to a not-static
// error, so the analyzer's static checks leave it to the simulator.
type UnsupportedError struct {
	What string
	err  error
}

func (e *UnsupportedError) Error() string { return e.err.Error() }

func (e *UnsupportedError) Unwrap() error { return e.err }

// Instance is one call's binding of the generics of a subprogram
// instance.
type Instance struct {
	// Of is the uninstantiated subprogram; Body is its body.
	Of, Body *Decl
	// Generics bind the body's generics, in order (the index and
	// element types of an array-class generic type after it).
	Generics []Generic
	fe       *Evaluator // the bound frame (sema's own interpreter)
	env      *instEnv
	key      string // the memo key of the bindings; "" none
}

// Generic is the binding of one generic of an instance body.
type Generic struct {
	// Decl is the body's generic: a constant, a type or a subprogram.
	Decl *Decl
	// Value is a generic constant's value; Layout its subtype, or a
	// generic type's actual subtype.
	Value  eval.Value
	Layout *eval.Type
	// Sub is a generic subprogram's actual.
	Sub *Decl
}

// instEnv holds the bindings of an instance's generics in a frame and
// the frames of the calls it makes. reads counts the reads of them: a
// memoized instance call may read its own bindings (they are part of
// its key), but no other.
type instEnv struct {
	consts  map[*Decl]eval.Value
	layouts map[*Decl]*eval.Type
	subs    map[*Decl]*Decl
	outer   *instEnv
	reads   int
}

// genConst returns the value of the generic constant d bound in ev's
// instance environments.
func (ev *Evaluator) genConst(d *Decl) (eval.Value, bool) {
	for g := ev.gen; g != nil; g = g.outer {
		if v, ok := g.consts[d]; ok {
			g.reads++
			ev.sh.reads++
			return v, true
		}
	}
	return eval.Value{}, false
}

// genLayout returns the layout of the generic type d bound in ev's
// instance environments or, for a frame the simulator runs, by its
// Dynamic.
func (ev *Evaluator) genLayout(d *Decl) (*eval.Type, bool) {
	for g := ev.gen; g != nil; g = g.outer {
		if lt, ok := g.layouts[d]; ok {
			g.reads++
			ev.sh.reads++
			return lt, true
		}
	}
	if gd, ok := ev.dyn.(GenericDynamic); ok {
		if lt, ok := gd.GenericLayout(d); ok {
			ev.sh.dynReads++
			return lt, true
		}
	}
	return nil, false
}

// genSub returns the actual of the generic subprogram d.
func (ev *Evaluator) genSub(d *Decl) (*Decl, bool) {
	for g := ev.gen; g != nil; g = g.outer {
		if a, ok := g.subs[d]; ok {
			g.reads++
			ev.sh.reads++
			return a, a != nil
		}
	}
	if gd, ok := ev.dyn.(GenericDynamic); ok {
		if a, ok := gd.GenericSubprogram(d); ok && a != nil {
			ev.sh.dynReads++
			return a, true
		}
	}
	return nil, false
}

// GenericDynamic is implemented by a Dynamic whose frames bind the
// generics of the subprogram instances it runs (the simulator's
// procedures and impure functions): the generic constants are objects
// of the frame (Dynamic.Ref), the generic types and subprograms are
// answered here.
type GenericDynamic interface {
	// GenericLayout returns the actual subtype of the generic type d
	// of a running instance.
	GenericLayout(d *Decl) (*eval.Type, bool)
	// GenericSubprogram returns the actual of the generic subprogram d
	// of a running instance.
	GenericSubprogram(d *Decl) (*Decl, bool)
}

// Instance binds the generics of the subprogram instance x for a call
// made in ev's context, charging b.
func (ev *Evaluator) Instance(x *Decl, b *eval.Budget) (*Instance, error) {
	return ev.instance(x, b)
}

func (ev *Evaluator) instance(x *Decl, b *eval.Budget) (*Instance, error) {
	g := InstanceOf(x)
	if g == nil {
		return nil, eval.NotStatic("call of " + x.Text)
	}
	body := ev.body(g)
	if body == nil || body.Region == nil {
		return nil, eval.NotStatic("call of " + x.Text)
	}
	if err := b.Step(1); err != nil {
		return nil, err
	}
	specs, gens := genericsOf(g.Region), genericsOf(body.Region)
	if len(specs) != len(gens) {
		return nil, eval.NotStatic("the generics of " + x.Text)
	}
	for _, d := range gens {
		if d.Kind == DeclGenericPackage {
			return nil, &UnsupportedError{What: cGenericPackageFormal,
				err: eval.NotStatic("generic package " + d.Text + " of " +
					x.Text)}
		}
	}
	env := &instEnv{consts: map[*Decl]eval.Value{},
		layouts: map[*Decl]*eval.Type{}, subs: map[*Decl]*Decl{},
		outer: ev.gen}
	in := &Instance{Of: g, Body: body, env: env}
	// fe evaluates defaults and subtypes that name earlier generics;
	// it becomes the frame of the call.
	fe := ev.enter()
	fe.gen = env
	fe.consts, fe.layouts = map[*Decl]constResult{}, map[*Type]*eval.Type{}
	in.fe = fe
	subs := ev.instanceSubs(x, g, body, specs, gens)
	key := make([]byte, 0, 32)
	keyed := x.Unit != nil
	if keyed {
		key = appendDecl(key, x)
	}
	n := 0
	for i, d := range gens {
		as := association(x, specs[i], d)
		switch d.Kind {
		case DeclGeneric:
			var v eval.Value
			var err error
			if as != nil && as.Actual != nil {
				v, err = ev.value(as.Actual, b)
			} else if def := d.Value; def != nil || specs[i].Value != nil {
				if def == nil {
					def = specs[i].Value
				}
				v, err = fe.value(def, b)
			} else {
				err = eval.NotStatic("generic " + d.Text + " of " +
					x.Text)
			}
			if err != nil {
				return nil, err
			}
			t, err := fe.layout(d.Type, b)
			if err != nil {
				return nil, err
			}
			t = eval.InferSubtype(t, v)
			if v, err = eval.Check(v, t); err != nil {
				return nil, err
			}
			env.consts[d] = v
			in.Generics = append(in.Generics,
				Generic{Decl: d, Value: v, Layout: t})
			if keyed {
				key = append(key, '|')
				key, keyed = appendValue(key, v, &n)
			}
		case DeclGenericType:
			if as == nil || as.Actual == nil || as.Actual.Type.Unknown() {
				return nil, eval.NotStatic("generic type " + d.Text +
					" of " + x.Text)
			}
			if err := ev.bindGenericType(in, d, as.Actual.Type, b); err != nil {
				return nil, err
			}
		case DeclGenericSubprogram:
			a := subs[i]
			env.subs[d] = a
			in.Generics = append(in.Generics, Generic{Decl: d, Sub: a})
		}
	}
	if keyed {
		for _, gb := range in.Generics {
			if gb.Decl.Kind == DeclGenericType {
				key = append(key, '|')
				key = appendLayout(key, gb.Layout, 0)
			}
		}
		in.key = string(key)
	}
	return in, nil
}

// bindGenericType binds the generic type d of in to the actual subtype
// t: its layout, and for an array-class generic type (LRM 6.5.3) the
// layouts of its anonymous index and element types.
func (ev *Evaluator) bindGenericType(in *Instance, d *Decl, t *Type,
	b *eval.Budget,
) error {
	lt, err := ev.layout(t, b)
	if err != nil {
		return err
	}
	in.env.layouts[d] = lt
	in.Generics = append(in.Generics, Generic{Decl: d, Layout: lt})
	g := d.Type
	if g == nil || g.Class != TypeGeneric || t.Base().Class != TypeArray {
		return nil
	}
	for i, x := range g.Index {
		if i < len(t.Base().Index) && x.Class == TypeGeneric &&
			x.Decl != nil {
			if err := ev.bindGenericType(in, x.Decl, t.Base().Index[i],
				b); err != nil {
				return err
			}
		}
	}
	if x := g.Elem; x != nil && x.Class == TypeGeneric && x.Decl != nil &&
		t.ElemType() != nil {
		return ev.bindGenericType(in, x.Decl, t.ElemType(), b)
	}
	return nil
}

// association returns the association of the instance x for the
// generic spec of the declaration (or gen of the body), nil when there
// is none.
func association(x *Decl, spec, gen *Decl) *Association {
	for i := range x.Generics {
		if f := x.Generics[i].Formal; f != nil && (f == spec || f == gen) {
			return &x.Generics[i]
		}
	}
	return nil
}

// InstanceSubprogram returns the actual of the generic subprogram d
// (a generic of the body of x's uninstantiated subprogram) in the
// instance x, nil when it is not known.
func (ev *Evaluator) InstanceSubprogram(x, d *Decl) *Decl {
	g := InstanceOf(x)
	if g == nil {
		return nil
	}
	body := ev.body(g)
	if body == nil {
		return nil
	}
	specs, gens := genericsOf(g.Region), genericsOf(body.Region)
	if len(specs) != len(gens) {
		return nil
	}
	subs := ev.instanceSubs(x, g, body, specs, gens)
	for i, gd := range gens {
		if gd == d || specs[i] == d {
			return subs[i]
		}
	}
	return nil
}

// instanceSubs returns, per generic position, the actual subprogram
// of each generic subprogram of the instance x: the associated
// subprogram, else the formal's default (is name, or is <>: the
// subprogram of the formal's designator visible where x is declared or
// declared with the actual types, LRM 6.5.6.2). Overloads are resolved
// by the formal's profile with the instance's generic types replaced
// by their actuals. Resolved once per instance.
func (ev *Evaluator) instanceSubs(x, g, body *Decl, specs, gens []*Decl,
) []*Decl {
	if s, ok := ev.sh.instSubs[x]; ok {
		return s
	}
	subst := map[*Type]*Type{} // lookup only
	for i, d := range gens {
		if d.Kind != DeclGenericType {
			continue
		}
		if as := association(x, specs[i], d); as != nil &&
			as.Actual != nil && !as.Actual.Type.Unknown() {
			subst[d.Type], subst[specs[i].Type] = as.Actual.Type,
				as.Actual.Type
		}
	}
	out := make([]*Decl, len(gens))
	for i, d := range gens {
		if d.Kind != DeclGenericSubprogram {
			continue
		}
		var cands []*Decl
		if as := association(x, specs[i], d); as != nil {
			if as.Actual != nil && as.Actual.Decl != nil {
				cands = append(cands, as.Actual.Decl)
			} else {
				cands = refsIn(x.Unit, as.Node.Span(), specs[i], d)
				// An operator symbol records no reference: the
				// subprograms of the actual's designator.
				act := cst.AssociationElement{Node: as.Node}.Actual()
				if !act.IsZero() {
					k := TokenKey(act.Node.LastToken())
					cands = append(cands, visibleSubs(x, k, specs[i],
						subst)...)
				}
			}
		} else {
			cands = defaultSubs(x, specs[i], d, subst)
		}
		out[i] = matchProfile(cands, d, subst)
	}
	if ev.sh.instSubs == nil {
		ev.sh.instSubs = map[*Decl][]*Decl{}
	}
	ev.sh.instSubs[x] = out
	return out
}

// refsIn returns the subprograms the names inside span of unit u
// denote or may denote (the overload candidates of a generic map
// actual), without the formals f and g.
func refsIn(u *Unit, span cst.Range, f, g *Decl) []*Decl {
	if u == nil {
		return nil
	}
	var out []*Decl
	for _, r := range u.Refs {
		if r.Span.Start < span.Start || r.Span.End > span.End ||
			r.Kind == RefFormal {
			continue
		}
		if r.Overload != nil {
			for _, c := range r.Overload.Candidates {
				out = append(out, c.Decl)
			}
		}
		if r.Decl != nil && r.Decl != f && r.Decl != g {
			out = append(out, r.Decl)
		}
	}
	return out
}

// defaultSubs returns the candidates of the default of the generic
// subprogram spec (gen in the body) of the instance x.
func defaultSubs(x, spec, gen *Decl, subst map[*Type]*Type) []*Decl {
	for _, f := range []*Decl{spec, gen} {
		for a := range f.Node.Ancestors() {
			if a.Kind() != cst.KindInterfaceSubprogramDecl {
				continue
			}
			def := cst.InterfaceSubprogramDecl{Node: a}.Default()
			if def.IsZero() {
				break
			}
			if n := def.Name(); !n.IsZero() {
				return refsIn(f.Unit, n.Span(), spec, gen)
			}
			// is <>: the subprograms of that designator.
			return visibleSubs(x, spec.Name, spec, subst)
		}
	}
	return nil
}

// visibleSubs returns the declarations named k in the regions around
// the instance x and in the regions declaring the actual types of the
// profile of the generic subprogram spec (where their predefined
// operations are).
func visibleSubs(x *Decl, k Key, spec *Decl, subst map[*Type]*Type,
) []*Decl {
	if k == "" {
		return nil
	}
	var out []*Decl
	at, from := siteOf(x)
	for r := from; r != nil; r = r.Parent {
		out = append(out, r.Lookup(k)...)
		for _, inc := range r.Includes {
			out = append(out, inc.Lookup(k)...)
		}
		for _, u := range r.Uses {
			if u.Off > at && r.File == x.File {
				continue
			}
			if u.All {
				out = append(out, u.Region.Lookup(k)...)
				continue
			}
			for _, d := range u.Decls {
				if d.Name == k {
					out = append(out, d)
				}
			}
		}
	}
	for _, p := range append(slices.Clone(spec.Params),
		&Decl{Type: spec.Type}) {
		t := p.Type
		if s, ok := subst[t]; ok {
			t = s
		}
		if t == nil || t.Base() == nil || t.Base().Decl == nil {
			continue
		}
		out = append(out, t.Base().Decl.Parent.Lookup(k)...)
	}
	return out
}

// siteOf returns where the instance x is made (LRM 6.5.6.2: an is <>
// default denotes the subprogram visible there): its offset and the
// innermost region of its unit around it (the call of an implicit
// instance, the declaration of a VHDL-2008 one); x.Parent when the
// unit is not known.
func siteOf(x *Decl) (int, *Region) {
	at := x.Node.Span().Start
	if x.Unit == nil || x.Unit.Region == nil || x.Node.IsZero() {
		return at, x.Parent
	}
	r := x.Unit.Region
	for {
		var in *Region
		for _, c := range r.Children {
			if c.File == x.File && c.Span.Start <= at && at < c.Span.End {
				in = c
				break
			}
		}
		if in == nil {
			return at, r
		}
		r = in
	}
}

// matchProfile returns the first candidate whose parameter and result
// base types are those of the generic subprogram d with the generic
// types replaced by subst (LRM 6.5.7.2), nil when none is.
func matchProfile(cands []*Decl, d *Decl, subst map[*Type]*Type) *Decl {
	sub := func(t *Type) *Type {
		if s, ok := subst[t]; ok {
			return s
		}
		if t != nil {
			if s, ok := subst[t.Base()]; ok {
				return s
			}
		}
		return t
	}
	same := func(a, w *Type) bool {
		return a.Unknown() || w.Unknown() || SameBase(a, sub(w))
	}
	for _, c := range cands {
		x := c.Denotes()
		if x == nil || !x.Overloadable() || x == d {
			continue
		}
		ps := profileParams(x)
		if len(ps) != len(d.Params) {
			continue
		}
		xr, dr := resultType(x), resultType(d)
		if (xr == nil) != (dr == nil) || (dr != nil && !same(xr, dr)) {
			continue
		}
		ok := true
		for i, p := range ps {
			if !same(p.Type, d.Params[i].Type) {
				ok = false
				break
			}
		}
		if ok {
			return c
		}
	}
	return nil
}

// Dispatch returns the call e of a generic subprogram rewritten as the
// call of its actual (LRM 6.5.6.2), with the actuals in parameter
// order; e itself when it calls no generic subprogram.
func (ev *Evaluator) Dispatch(e *Expr) (*Expr, error) {
	if e == nil || e.Decl == nil {
		return e, nil
	}
	x := e.Decl.Denotes()
	if x.Kind != DeclGenericSubprogram {
		return e, nil
	}
	a, ok := ev.genSub(x)
	if !ok {
		return nil, eval.NotStatic("the actual of generic subprogram " +
			x.Text)
	}
	acts, err := actuals(e, x)
	if err != nil {
		return nil, err
	}
	if ax := a.Denotes(); !ax.Implicit && len(acts) > len(ax.Params) {
		return nil, eval.NotStatic("call of " + ax.Text)
	}
	return &Expr{Kind: e.Kind, Node: e.Node, Decl: a, Args: acts,
		Type: e.Type}, nil
}
