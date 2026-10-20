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
	d, ok := fn.(*Decl)
	if !ok {
		return eval.Value{}, eval.NotStatic("call")
	}
	x := d.Denotes()
	if x.Kind != DeclFunction || !x.Pure {
		return eval.Value{}, eval.NotStatic("call of " + x.Text)
	}
	if ev.memo {
		return ev.memoCall(x, args, b) // memo.go
	}
	return ev.pureCall(x, args, b)
}

// pureCall interprets the body of the pure function x on args.
func (ev *Evaluator) pureCall(x *Decl, args []eval.Value, b *eval.Budget,
) (eval.Value, error) {
	out, err := ev.invoke(x, args, nil, b)
	if err != nil {
		return eval.Value{}, err
	}
	if out.flow != flowReturn {
		return eval.Value{}, eval.NotStatic("function " + x.Text +
			" ended without return")
	}
	return out.v, nil
}

// invoke runs the body of the subprogram x with the parameter values
// args (in parameter order). For a procedure, outs receives the final
// values of the parameters.
func (ev *Evaluator) invoke(x *Decl, args []eval.Value, outs []eval.Value,
	b *eval.Budget,
) (outcome, error) {
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
			v, err := ev.value(br.Value, b)
			if err != nil {
				return outcome{}, err
			}
			return outcome{flow: flowReturn, v: v}, nil
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
		return eval.Value{T: old.T, B: old.B, E: v.E}, nil
	case eval.KindRecord:
		return eval.Value{T: old.T, E: v.E}, nil
	case eval.KindInvalid:
		return v, nil
	}
	if t == nil {
		t = old.T
	}
	return eval.Check(v, t)
}

// procedure interprets a procedure call statement: in parameters are
// evaluated, out and inout parameters are variables of the caller,
// written back when the procedure returns.
func (ev *Evaluator) procedure(e *Expr, b *eval.Budget) error {
	if e == nil || e.Decl == nil {
		return eval.NotStatic("procedure call")
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
	if _, err := ev.invoke(x, args, outs, b); err != nil {
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
