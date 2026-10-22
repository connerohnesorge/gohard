package sema

// static.go is static evaluation (#34): it lowers typed expressions to
// the evaluator IR of internal/eval (the one evaluator the simulator
// shares, #35) and evaluates them under a step budget, with per-instance
// generic values bound by internal/elab/hier. The unit-level check
// below evaluates the constants that need no instance; per-instance
// checks (widths, ranges that depend on generics) are hier's. This file
// is the extension point for static semantics (user function bodies,
// more attributes, per-instance checks).

import (
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Evaluator evaluates typed expressions statically. An Evaluator with
// bindings is one instance's view: generics, generate parameters and
// generic types bound to their values. It caches constants and type
// layouts, so one Evaluator belongs to one job.
type Evaluator struct {
	steps   int64
	libs    func(Key) (*Library, bool)
	bind    map[*Decl]eval.Value
	types   map[*Decl]*Type
	consts  map[*Decl]constResult
	layouts map[*Type]*eval.Type
	active  map[*Decl]bool
	// busy holds the types whose layout is being computed: a type
	// that contains itself has no layout (sema reports it).
	busy map[*Type]bool
	sh   *shared
	// fr is the frame of an interpreted call (interp.go), nil for the
	// instance's own evaluator.
	fr   *frame
	used int64
	// dyn is the simulator's run-time context (dynamic.go); nil for
	// static evaluation.
	dyn Dynamic
	// memo enables the memoization of pure calls (memo.go).
	memo bool
	// std is the standard of the analysis: it picks the layout of
	// std.standard's INTEGER, NATURAL and POSITIVE (integerLayout).
	std token.Standard
	// gen binds the generics of the subprogram instance whose call ev
	// interprets (interp.go); its consts and layouts caches are then
	// the call's own.
	gen *instEnv
}

type constResult struct {
	v   eval.Value
	err error
}

// NewEvaluator returns an evaluator over the analyzed result r with a
// budget of steps per evaluation (<= 0: eval.EditorSteps). It
// evaluates under r's standard: INTEGER is 64 bits wide under
// VHDL-2019 and 32 bits before (eval.StdIntegers).
func NewEvaluator(r *Result, steps int64) *Evaluator {
	ev := newEvaluator(r.Library, steps)
	ev.std = r.Standard
	return ev
}

func newEvaluator(libs func(Key) (*Library, bool), steps int64,
) *Evaluator {
	if steps <= 0 {
		steps = eval.EditorSteps
	}
	return &Evaluator{steps: steps, libs: libs,
		bind: map[*Decl]eval.Value{}, types: map[*Decl]*Type{},
		consts: map[*Decl]constResult{}, layouts: map[*Type]*eval.Type{},
		active: map[*Decl]bool{},
		sh:     newShared()}
}

// Fresh returns an evaluator without bindings that shares ev's
// subprogram index: the evaluator of another instance of the same
// design.
func (ev *Evaluator) Fresh() *Evaluator {
	c := newEvaluator(ev.libs, ev.steps)
	c.sh, c.std = ev.sh, ev.std
	return c
}

// Bind returns a copy of ev in which d (a generic, generate or loop
// parameter) has the value v.
func (ev *Evaluator) Bind(d *Decl, v eval.Value) *Evaluator {
	c := ev.fork()
	c.bind[d] = v
	return c
}

// BindType returns a copy of ev in which the generic type d is t. The
// anonymous index and element types of a VHDL-2019 array class
// generic type (genericArrayClass) are bound to t's.
func (ev *Evaluator) BindType(d *Decl, t *Type) *Evaluator {
	c := ev.fork()
	c.bindType(d, t)
	return c
}

func (ev *Evaluator) bindType(d *Decl, t *Type) {
	ev.types[d] = t
	g := d.Type
	if g == nil || g.Class != TypeGeneric || t.Unknown() ||
		t.Base().Class != TypeArray {
		return
	}
	for i, x := range g.Index {
		if i < len(t.Base().Index) && x.Class == TypeGeneric &&
			x.Decl != nil {
			ev.bindType(x.Decl, t.Base().Index[i])
		}
	}
	if x := g.Elem; x != nil && x.Class == TypeGeneric && x.Decl != nil &&
		t.ElemType() != nil {
		ev.bindType(x.Decl, t.ElemType())
	}
}

func (ev *Evaluator) fork() *Evaluator {
	c := ev.Fresh()
	maps.Copy(c.bind, ev.bind)
	maps.Copy(c.types, ev.types)
	return c
}

// Standard returns the standard ev evaluates under.
func (ev *Evaluator) Standard() token.Standard { return ev.std }

// Used returns the steps the last evaluation used.
func (ev *Evaluator) Used() int64 { return ev.used }

// Value evaluates e with a fresh budget.
func (ev *Evaluator) Value(e *Expr) (eval.Value, error) {
	b := eval.NewBudget(ev.steps)
	v, err := ev.value(e, b)
	ev.used = b.Used()
	return v, err
}

// Constant evaluates the constant d (the full declaration's value for
// a deferred constant) checked against its subtype, once per
// evaluator.
func (ev *Evaluator) Constant(d *Decl) (eval.Value, error) {
	b := eval.NewBudget(ev.steps)
	v, err := ev.constant(d, b)
	ev.used = b.Used()
	return v, err
}

// Track runs f and reports whether the evaluations f makes with ev
// depend on ev's bindings (generics, generate parameters, generic
// types): an error they find is the instance's, while an
// instance-independent one is reported once with its unit.
func (ev *Evaluator) Track(f func()) bool {
	r := ev.sh.reads
	f()
	return ev.sh.reads != r
}

// CheckName checks the static indices and slice bounds of the name e
// against the index ranges of each prefix's subtype, so an actual such
// as din(HI downto LO) whose bounds leave din's range is an error
// before anything runs (#34). Indices that are not static are not
// checked; the error is nil then, or the step limit.
func (ev *Evaluator) CheckName(e *Expr) error {
	b := eval.NewBudget(ev.steps)
	err := ev.checkName(e, b)
	ev.used = b.Used()
	return err
}

func (ev *Evaluator) checkName(e *Expr, b *eval.Budget) error {
	if e == nil {
		return nil
	}
	switch e.Kind {
	case ExprSelect:
		return ev.checkName(e.Prefix, b)
	case ExprIndex, ExprSlice:
	default:
		return nil
	}
	if err := ev.checkName(e.Prefix, b); err != nil {
		return err
	}
	pt := e.Prefix.Type
	if pt == nil || pt.Base().Class != TypeArray ||
		pt.Constraints() == nil {
		return nil
	}
	lt, err := ev.layout(pt, b)
	if err != nil || lt.Bounds == nil {
		return limitOnly(err)
	}
	if e.Kind == ExprIndex {
		for i, a := range e.Args {
			if i >= len(lt.Bounds) {
				break
			}
			v, err := ev.value(a, b)
			if err != nil {
				return designOrLimit(err)
			}
			if !lt.Bounds[i].Contains(v.I) {
				return eval.IndexError(v.I, lt.Bounds[i])
			}
		}
		return nil
	}
	r, err := ev.rangeOf(e.Range, b)
	if err != nil {
		return designOrLimit(err)
	}
	ar := lt.Bounds[0]
	if r.Null() {
		return nil
	}
	if r.Dir != ar.Dir || !ar.Contains(r.Left) || !ar.Contains(r.Right) {
		return eval.SliceError(r, ar)
	}
	return nil
}

// limitOnly keeps only the step-limit error.
func limitOnly(err error) error {
	if eval.CodeOf(err) == eval.ErrCodeBudget {
		return err
	}
	return nil
}

// designOrLimit keeps design errors and the step limit, dropping "not
// static".
func designOrLimit(err error) error {
	if c := eval.CodeOf(err); c == eval.ErrCodeBudget || DesignError(c) {
		return err
	}
	return nil
}

// Layout returns the evaluated layout of t (its bounds evaluated).
func (ev *Evaluator) Layout(t *Type) (*eval.Type, error) {
	b := eval.NewBudget(ev.steps)
	lt, err := ev.layout(t, b)
	ev.used = b.Used()
	return lt, err
}

// Range evaluates a range constraint.
func (ev *Evaluator) Range(rc *RangeCons) (eval.Range, error) {
	b := eval.NewBudget(ev.steps)
	r, err := ev.rangeOf(rc, b)
	ev.used = b.Used()
	return r, err
}

func (ev *Evaluator) value(e *Expr, b *eval.Budget) (eval.Value, error) {
	x, err := ev.lower(e, b)
	if err != nil {
		return eval.Value{}, err
	}
	m := &eval.Machine{Env: ev, Budget: b}
	return m.Eval(x)
}

// Load implements eval.Env: bound objects and the cells of an
// interpreted call are static here.
func (ev *Evaluator) Load(ref eval.Ref) (eval.Value, error) {
	if _, ok := ref.(*Decl); !ok && ev.dyn != nil {
		ev.sh.dynReads++
		return ev.dyn.Load(ref)
	}
	if d, ok := ref.(*Decl); ok {
		if c := ev.cell(d); c != nil {
			return c.v, nil
		}
		ev.sh.reads++
		if v, ok := ev.bind[d]; ok {
			return v, nil
		}
		return eval.Value{}, eval.NotStatic(d.Text)
	}
	ev.sh.reads++
	return eval.Value{}, eval.NotStatic("object")
}

// cell returns the frame cell of d (nil outside a frame or when d is
// not an object of the call), counting the read.
func (ev *Evaluator) cell(d *Decl) *cell {
	if ev.fr == nil {
		return nil
	}
	c, ok := ev.fr.vars[d]
	if !ok {
		return nil
	}
	ev.fr.loads++
	c.owned = false // the value escapes: copy before writing elements
	return c
}

// lower turns a typed expression into evaluator IR. Constants are
// evaluated (once) while lowering, so the IR stays as small as the
// expression.
func (ev *Evaluator) lower(e *Expr, b *eval.Budget) (eval.Expr, error) {
	if e == nil {
		return nil, eval.NotStatic("missing expression")
	}
	if e.illTyped {
		// Its type error is reported; a value would only cascade.
		return nil, eval.NotStatic("an ill-typed expression")
	}
	if err := b.Step(1); err != nil {
		return nil, err
	}
	switch e.Kind {
	case ExprLiteral:
		return ev.literal(e, b)
	case ExprPhysical:
		return ev.physical(e, b)
	case ExprName:
		return ev.name(e, b)
	case ExprCall:
		return ev.call(e, b)
	case ExprQualified, ExprConversion:
		if len(e.Args) != 1 {
			return nil, eval.NotStatic("conversion")
		}
		x, err := ev.lower(e.Args[0], b)
		if err != nil {
			return nil, err
		}
		t, err := ev.layout(e.Type, b)
		if err != nil {
			return nil, err
		}
		if e.Kind == ExprQualified {
			return &eval.Qualify{X: x, T: t}, nil
		}
		return &eval.Convert{X: x, T: t}, nil
	case ExprAttr:
		return ev.attr(e, b)
	case ExprIndex:
		x, err := ev.lower(e.Prefix, b)
		if err != nil {
			return nil, err
		}
		ix := &eval.Index{X: x}
		for _, a := range e.Args {
			ax, err := ev.lower(a, b)
			if err != nil {
				return nil, err
			}
			ix.Idx = append(ix.Idx, ax)
		}
		return ix, nil
	case ExprSlice:
		x, err := ev.lower(e.Prefix, b)
		if err != nil {
			return nil, err
		}
		r, err := ev.rangeIR(e.Range, b)
		if err != nil {
			return nil, err
		}
		return &eval.Slice{X: x, R: r}, nil
	case ExprSelect:
		x, err := ev.lower(e.Prefix, b)
		if err != nil {
			return nil, err
		}
		return &eval.Element{X: x, N: e.Decl.Pos}, nil
	case ExprAggregate:
		return ev.aggregate(e, b)
	case ExprCond:
		c := &eval.Cond{}
		for i := 0; i+1 < len(e.Args); i += 2 {
			v, err := ev.lower(e.Args[i], b)
			if err != nil {
				return nil, err
			}
			k, err := ev.lower(e.Args[i+1], b)
			if err != nil {
				return nil, err
			}
			c.Values, c.Conds = append(c.Values, v), append(c.Conds, k)
		}
		if len(e.Args)%2 == 1 {
			x, err := ev.lower(e.Args[len(e.Args)-1], b)
			if err != nil {
				return nil, err
			}
			c.Else = x
		}
		return c, nil
	}
	if l, ok, err := ev.dynLower(e, b); ok {
		return l, err
	}
	return nil, eval.NotStatic("expression")
}

func (ev *Evaluator) literal(e *Expr, b *eval.Budget) (eval.Expr, error) {
	text := e.Lit.Text()
	switch e.Lit.Kind() {
	case token.IntegerLiteral:
		v, ok := ParseInteger(text)
		if !ok {
			return nil, eval.Unsupported("integer literal " + text)
		}
		return &eval.Const{V: eval.Int(eval.UniversalInteger, v)}, nil
	case token.RealLiteral:
		f, ok := ParseReal(text)
		if !ok {
			return nil, eval.Unsupported("real literal " + text)
		}
		return &eval.Const{V: eval.RealValue(eval.UniversalReal, f)}, nil
	case token.StringLiteral, token.BitStringLiteral:
		t, err := ev.layout(e.Type, b)
		if err != nil {
			return nil, err
		}
		s := text
		if e.Lit.Kind() == token.BitStringLiteral {
			var ok bool
			if s, ok = ExpandBitString(text, maxBits); !ok {
				return nil, eval.Unsupported("bit string " + text)
			}
			if err := b.Step(int64(len(s))); err != nil {
				return nil, err
			}
		} else if len(s) >= 2 {
			s = strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
		}
		return stringConst(t, s)
	case token.KwNull:
		t, err := ev.layout(e.Type, b)
		if err != nil {
			return nil, err
		}
		return &eval.Const{V: eval.Value{T: t}}, nil
	}
	return nil, eval.NotStatic("literal")
}

// stringConst builds the array value of a string literal of type t.
func stringConst(t *eval.Type, s string) (eval.Expr, error) {
	bt := t.BaseType()
	if bt.Kind != eval.KindArray || bt.Elem == nil || len(bt.Index) != 1 {
		return nil, eval.NotStatic("string literal")
	}
	lits := bt.Elem.BaseType().Literals
	el := make([]eval.Value, len(s))
	for i := range len(s) {
		img := string([]byte{'\'', s[i], '\''})
		pos := -1
		for j, l := range lits {
			if l == img {
				pos = j
				break
			}
		}
		if pos < 0 {
			return nil, eval.NotStatic("string literal character")
		}
		el[i] = eval.Enum(bt.Elem, int64(pos))
	}
	r := eval.Range{Left: bt.Index[0].Range.Left,
		Dir: bt.Index[0].Range.Dir}
	if t.Bounds != nil {
		r = eval.Range{Left: t.Bounds[0].Left, Dir: t.Bounds[0].Dir}
	}
	r.Right = r.At(int64(len(s)) - 1)
	return &eval.Const{V: eval.Array(t, []eval.Range{r}, el)}, nil
}

func (ev *Evaluator) physical(e *Expr, b *eval.Budget) (eval.Expr, error) {
	if e.Decl == nil {
		return nil, eval.NotStatic("physical literal")
	}
	t, err := ev.layout(e.Type, b)
	if err != nil {
		return nil, err
	}
	f, err := ev.unitFactor(e.Decl.Denotes(), b, 0)
	if err != nil {
		return nil, err
	}
	text := e.Lit.Text()
	if e.Lit.IsZero() {
		text = "1"
	}
	if iv, ok := ParseInteger(text); ok {
		v, err := eval.Apply(b, eval.OpMul, eval.UniversalInteger,
			eval.Int(eval.UniversalInteger, iv),
			eval.Int(eval.UniversalInteger, f))
		if err != nil {
			return nil, err
		}
		return &eval.Const{V: eval.Phys(t, v.I)}, nil
	}
	fv, ok := ParseReal(text)
	if !ok {
		return nil, eval.Unsupported("physical literal " + text)
	}
	x := math.Round(fv * float64(f))
	if x >= math.MaxInt64 || x < math.MinInt64 {
		return nil, eval.RangeError(eval.RealValue(eval.Real, fv), t)
	}
	return &eval.Const{V: eval.Phys(t, int64(x))}, nil
}

// unitFactor returns the value of a physical unit in primary units.
func (ev *Evaluator) unitFactor(u *Decl, b *eval.Budget, depth int,
) (int64, error) {
	if u.Value == nil {
		return 1, nil // the primary unit
	}
	if depth > 64 {
		return 0, eval.NotStatic("unit " + u.Text)
	}
	if err := b.Step(1); err != nil {
		return 0, err
	}
	v := u.Value
	if v.Kind != ExprPhysical || v.Decl == nil {
		return 0, eval.NotStatic("unit " + u.Text)
	}
	f, err := ev.unitFactor(v.Decl.Denotes(), b, depth+1)
	if err != nil {
		return 0, err
	}
	n, ok := ParseInteger(v.Lit.Text())
	if v.Lit.IsZero() {
		n, ok = 1, true
	}
	if !ok {
		return 0, eval.NotStatic("unit " + u.Text)
	}
	r, err := eval.Apply(b, eval.OpMul, eval.UniversalInteger,
		eval.Int(eval.UniversalInteger, n),
		eval.Int(eval.UniversalInteger, f))
	return r.I, err
}

func (ev *Evaluator) name(e *Expr, b *eval.Budget) (eval.Expr, error) {
	d := e.Decl
	if d == nil {
		// An external name: the simulator resolves it (dynamic.go).
		if l, ok, err := ev.dynLower(e, b); ok {
			return l, err
		}
		return nil, eval.NotStatic("name")
	}
	if c := ev.cell(d); c != nil {
		return &eval.Const{V: c.v}, nil
	}
	if v, ok := ev.bind[d]; ok {
		ev.sh.reads++
		return &eval.Const{V: v}, nil
	}
	x := d.Denotes()
	if c := ev.cell(x); c != nil {
		return &eval.Const{V: c.v}, nil
	}
	if v, ok := ev.bind[x]; ok {
		ev.sh.reads++
		return &eval.Const{V: v}, nil
	}
	if x.Kind == DeclGeneric {
		if v, ok := ev.genConst(x); ok {
			return &eval.Const{V: v}, nil
		}
	}
	if l, ok := ev.dynRef(x); ok {
		return l, nil
	}
	if packageGeneric(x) {
		ev.sh.reads++ // another instance may bind it
		return nil, &UnsupportedError{What: cPackageInstanceGeneric,
			err: eval.NotStatic(x.Text)}
	}
	switch x.Kind {
	case DeclEnumLiteral:
		t, err := ev.layout(x.Type, b)
		if err != nil {
			return nil, err
		}
		return &eval.Const{V: eval.Enum(t, int64(x.Pos))}, nil
	case DeclUnit:
		t, err := ev.layout(x.Type, b)
		if err != nil {
			return nil, err
		}
		f, err := ev.unitFactor(x, b, 0)
		if err != nil {
			return nil, err
		}
		return &eval.Const{V: eval.Phys(t, f)}, nil
	case DeclConstant:
		v, err := ev.constant(x, b)
		if err != nil {
			if DesignError(eval.CodeOf(err)) {
				// The constant's own error is reported where it is
				// declared; what reads it is not static, not wrong
				// a second time.
				return nil, eval.NotStatic("the erroneous constant " +
					x.Text)
			}
			return nil, err
		}
		return &eval.Const{V: v}, nil
	case DeclAlias:
		if x.Value != nil {
			return ev.aliased(x, b)
		}
	}
	if d.Kind == DeclAlias && d.Value != nil {
		return ev.aliased(d, b)
	}
	ev.sh.reads++ // another instance may bind it
	return &eval.Load{Ref: x}, nil
}

// aliased lowers the object an object alias denotes, with the alias's
// own subtype when it declares one (its index ranges, LRM 6.6.2).
func (ev *Evaluator) aliased(d *Decl, b *eval.Budget) (eval.Expr, error) {
	x, err := ev.lower(d.Value, b)
	if err != nil || d.Type == nil || d.Type == d.Value.Type {
		return x, err
	}
	t, err := ev.layout(d.Type, b)
	if err != nil {
		return nil, err
	}
	return &eval.Qualify{X: x, T: t}, nil
}

// constant evaluates a constant's value once per evaluator, finding the
// full declaration of a deferred constant in its package body.
func (ev *Evaluator) constant(d *Decl, b *eval.Budget) (eval.Value, error) {
	if r, ok := ev.sh.consts[d]; ok {
		return r.v, r.err
	}
	if r, ok := ev.consts[d]; ok {
		ev.sh.reads++ // it depends on a binding
		return r.v, r.err
	}
	if ev.fr != nil {
		if r, ok := ev.fr.consts[d]; ok {
			ev.fr.loads++
			return r.v, r.err
		}
	}
	if ev.active[d] {
		return eval.Value{}, eval.NotStatic(d.Text + " (circular)")
	}
	ev.active[d] = true
	defer delete(ev.active, d)
	reads, loads, dyn := ev.sh.reads, ev.loads(), ev.sh.dynReads
	v, err := ev.constantValue(d, b)
	if c := eval.CodeOf(err); c != eval.ErrCodeBudget &&
		ev.sh.dynReads == dyn {
		frameFree := ev.fr == nil || ev.fr.loads == loads
		switch {
		case ev.sh.reads == reads && frameFree:
			ev.sh.consts[d] = constResult{v, err}
			ev.sh.fills++ // memo.go: a cold computation
		case frameFree:
			ev.consts[d] = constResult{v, err}
			if ev.gen == nil { // an instance call's caches are its own
				ev.sh.fills++
			}
		default:
			if ev.fr.consts == nil {
				ev.fr.consts = map[*Decl]constResult{}
			}
			ev.fr.consts[d] = constResult{v, err}
		}
	}
	return v, err
}

func (ev *Evaluator) constantValue(d *Decl, b *eval.Budget,
) (eval.Value, error) {
	val := d.Value
	if val == nil {
		full := ev.fullConstant(d)
		if full == nil || full.Value == nil {
			return eval.Value{}, eval.NotStatic("deferred constant " +
				d.Text)
		}
		val = full.Value
	}
	v, err := ev.value(val, b)
	if err != nil {
		return v, err
	}
	if t, terr := ev.layout(d.Type, b); terr == nil {
		return eval.Check(v, t)
	}
	return v, nil
}

// fullConstant finds the package body declaration of a deferred
// constant.
func (ev *Evaluator) fullConstant(d *Decl) *Decl {
	if d.Parent == nil || d.Parent.Owner == nil || d.Unit == nil ||
		ev.libs == nil {
		return nil
	}
	pkg := d.Parent.Owner
	l, ok := ev.libs(d.Unit.Library)
	if !ok {
		return nil
	}
	for _, u := range l.Secondary(pkg.Name) {
		if u.Kind != UnitPackageBody || u.Region == nil {
			continue
		}
		for _, f := range u.Region.Lookup(d.Name) {
			if f.Kind == DeclConstant && f.Spec == d {
				return f
			}
		}
	}
	return nil
}

func (ev *Evaluator) call(e *Expr, b *eval.Budget) (eval.Expr, error) {
	d := e.Decl
	if d == nil {
		return nil, eval.NotStatic("call")
	}
	x := d.Denotes()
	if x.Kind == DeclGenericSubprogram {
		// A generic subprogram of an instance: its actual's call.
		de, err := ev.Dispatch(e)
		if err != nil {
			return nil, err
		}
		return ev.call(de, b)
	}
	if x.Kind == DeclEnumLiteral {
		return ev.name(&Expr{Kind: ExprName, Decl: d, Type: x.Type}, b)
	}
	if l, ok, err := ev.dynLower(e, b); ok {
		return l, err
	}
	if l, ok, err := ev.native(e, x, b); ok {
		return l, err
	}
	if x.Kind != DeclFunction || (x.Implicit && x.Op == 0) || !Pure(x) {
		return nil, eval.NotStatic("call of " + x.Text)
	}
	acts := e.Args
	if !x.Implicit {
		var err error
		if acts, err = actuals(e, x); err != nil {
			return nil, err
		}
	}
	var args []eval.Expr
	for _, a := range acts {
		ax, err := ev.lower(a, b)
		if err != nil {
			return nil, err
		}
		args = append(args, ax)
	}
	rt, err := ev.layout(orUnknown(x.Type), b)
	if err != nil {
		return nil, err
	}
	if x.Implicit {
		return &eval.Operation{Op: eval.Op(x.Op), Args: args, T: rt}, nil
	}
	return &eval.Call{Fn: x, Args: args, T: rt}, nil
}

// attrKinds maps attribute designators to evaluator attributes.
var attrKinds = map[Key]eval.AttrKind{
	"left": eval.AttrLeft, "right": eval.AttrRight,
	"high": eval.AttrHigh, "low": eval.AttrLow,
	"length": eval.AttrLength, "ascending": eval.AttrAscending,
	"pos": eval.AttrPos, "val": eval.AttrVal, "succ": eval.AttrSucc,
	"pred": eval.AttrPred, "leftof": eval.AttrLeftof,
	"rightof": eval.AttrRightof, "image": eval.AttrImage,
	"value": eval.AttrValue,
}

func (ev *Evaluator) attr(e *Expr, b *eval.Budget) (eval.Expr, error) {
	if l, ok, err := ev.dynLower(e, b); ok {
		return l, err
	}
	k, ok := attrKinds[e.Attr]
	if !ok || e.Prefix == nil {
		return nil, eval.NotStatic("'" + string(e.Attr))
	}
	at := &eval.Attr{Kind: k}
	if !e.Type.Unknown() {
		rt, err := ev.layout(e.Type, b)
		if err != nil {
			return nil, err
		}
		at.T = rt
	}
	argIsDim := k <= eval.AttrAscending
	for _, a := range e.Args {
		ax, err := ev.lower(a, b)
		if err != nil {
			return nil, err
		}
		if argIsDim {
			m := &eval.Machine{Env: ev, Budget: b}
			dv, err := m.Eval(ax)
			if err != nil {
				return nil, err
			}
			at.Dim = int(dv.I) - 1
		} else {
			at.Arg = ax
		}
	}
	p := e.Prefix
	pt := p.Type
	if k == eval.AttrImage && len(e.Args) == 0 &&
		p.Kind != ExprTypeMark {
		// O'IMAGE (VHDL-2019, LRM 16.2.2) images the object's value,
		// scalar or composite: its type alone says nothing.
		x, err := ev.lower(p, b)
		if err != nil {
			return nil, err
		}
		at.Of = x
		return at, nil
	}
	// A prefix whose subtype is constrained is static through its type
	// even when the object (a signal, a port) is not.
	if p.Kind == ExprTypeMark || (pt != nil && pt.IsScalar()) ||
		(pt != nil && pt.Base().Class == TypeArray &&
			pt.Constraints() != nil &&
			!slices.Contains(pt.Constraints(), nil)) {
		t, err := ev.layout(pt, b)
		if err != nil {
			return nil, err
		}
		at.Type = t
		return at, nil
	}
	x, err := ev.lower(p, b)
	if err != nil {
		return nil, err
	}
	at.Of = x
	return at, nil
}

// rangeIR lowers a range constraint to the evaluator's RangeExpr.
func (ev *Evaluator) rangeIR(rc *RangeCons, b *eval.Budget,
) (eval.RangeExpr, error) {
	if rc == nil {
		return eval.RangeExpr{}, eval.NotStatic("open range")
	}
	if rc.Left != nil && rc.Right != nil {
		l, err := ev.lower(rc.Left, b)
		if err != nil {
			return eval.RangeExpr{}, err
		}
		r, err := ev.lower(rc.Right, b)
		if err != nil {
			return eval.RangeExpr{}, err
		}
		return eval.RangeExpr{Left: l, Right: r, Dir: rc.Dir}, nil
	}
	a := rc.Attr
	if a == nil {
		return eval.RangeExpr{}, eval.NotStatic("range")
	}
	if a.Kind == ExprTypeMark {
		t, err := ev.layout(a.Type, b)
		if err != nil {
			return eval.RangeExpr{}, err
		}
		return eval.RangeExpr{Type: t}, nil
	}
	if a.Kind != ExprAttr || a.Prefix == nil {
		return eval.RangeExpr{}, eval.NotStatic("range")
	}
	out := eval.RangeExpr{Reverse: a.Attr == "reverse_range"}
	for _, x := range a.Args {
		v, err := ev.value(x, b)
		if err != nil {
			return eval.RangeExpr{}, err
		}
		out.Dim = int(v.I) - 1
	}
	p := a.Prefix
	pt := p.Type
	if p.Kind == ExprTypeMark || (pt != nil && (pt.IsScalar() ||
		pt.Constraints() != nil &&
			!slices.Contains(pt.Constraints(), nil))) {
		t, err := ev.layout(pt, b)
		if err != nil {
			return eval.RangeExpr{}, err
		}
		if t.Kind == eval.KindArray {
			if t.Bounds == nil || out.Dim >= len(t.Bounds) {
				return eval.RangeExpr{}, eval.NotStatic("'range")
			}
			s := t.Index[min(out.Dim, len(t.Index)-1)].Subtype(
				t.Bounds[out.Dim])
			out.Type, out.Dim = s, 0
			return out, nil
		}
		out.Type = t
		return out, nil
	}
	x, err := ev.lower(p, b)
	if err != nil {
		return eval.RangeExpr{}, err
	}
	out.Of = x
	return out, nil
}

func (ev *Evaluator) rangeOf(rc *RangeCons, b *eval.Budget,
) (eval.Range, error) {
	r, err := ev.rangeIR(rc, b)
	if err != nil {
		return eval.Range{}, err
	}
	m := &eval.Machine{Env: ev, Budget: b}
	return m.EvalRange(r)
}

// realRangeOf evaluates the range constraint rc of a floating type or
// subtype charging b: literal or expression bounds, a floating type
// mark, or a scalar 'RANGE or 'REVERSE_RANGE (LRM 5.2.5).
func (ev *Evaluator) realRangeOf(rc *RangeCons, b *eval.Budget,
) (eval.RealRange, error) {
	if rc == nil {
		return eval.RealRange{}, eval.NotStatic("floating range")
	}
	if rc.Left != nil && rc.Right != nil {
		l, err := ev.value(rc.Left, b)
		if err != nil {
			return eval.RealRange{}, err
		}
		r, err := ev.value(rc.Right, b)
		if err != nil {
			return eval.RealRange{}, err
		}
		if l.Kind() != eval.KindFloating || r.Kind() != eval.KindFloating {
			return eval.RealRange{}, eval.NotStatic("floating range")
		}
		return eval.RealRange{Left: l.F, Right: r.F, Dir: rc.Dir}, nil
	}
	a := rc.Attr
	if a == nil {
		return eval.RealRange{}, eval.NotStatic("floating range")
	}
	reverse := false
	if a.Kind == ExprAttr && a.Prefix != nil && len(a.Args) == 0 &&
		(a.Attr == "range" || a.Attr == "reverse_range") {
		reverse = a.Attr == "reverse_range"
		a = a.Prefix
	}
	if a.Type == nil || a.Type.Unknown() || (a.Kind != ExprTypeMark &&
		a.Kind != ExprName && a.Kind != ExprSelect) {
		return eval.RealRange{}, eval.NotStatic("floating range")
	}
	t, err := ev.layout(a.Type, b)
	if err != nil {
		return eval.RealRange{}, err
	}
	if t.Kind != eval.KindFloating {
		return eval.RealRange{}, eval.NotStatic("floating range")
	}
	r := t.Real
	if reverse {
		r = eval.RealRange{Left: r.Right, Right: r.Left, Dir: 1 - r.Dir}
	}
	return r, nil
}

// realCompatible checks that the bounds of the non-null floating
// range r belong to t (LRM 5.2.1).
func realCompatible(r eval.RealRange, t *eval.Type) error {
	if t == nil || t.Kind != eval.KindFloating || r.Low() > r.High() {
		return nil
	}
	for _, v := range []float64{r.Left, r.Right} {
		if !t.Real.Contains(v) {
			return eval.RangeError(eval.RealValue(t, v), t)
		}
	}
	return nil
}

func (ev *Evaluator) aggregate(e *Expr, b *eval.Budget) (eval.Expr, error) {
	t, err := ev.layout(e.Type, b)
	if err != nil {
		return nil, err
	}
	ag := &eval.Aggregate{T: t}
	for _, as := range e.Assocs {
		if as.Slice {
			// A VHDL-2008 slice association (types.go); the evaluator
			// has no slice choice yet.
			return nil, eval.NotStatic("a slice association")
		}
		v, err := ev.lower(as.Value, b)
		if err != nil {
			return nil, err
		}
		if et := elemSubtype(t, as.Choices); et != nil {
			// Each element value belongs to the element subtype
			// (LRM 9.3.3.3, 9.3.3.2).
			v = &eval.Qualify{X: v, T: et}
		}
		x := eval.Assoc{Value: v}
		if len(as.Choices) == 0 {
			x.Choices = []eval.Choice{{Kind: eval.ChoicePositional}}
		}
		for _, c := range as.Choices {
			switch {
			case c.Kind == ExprOthers:
				x.Choices = append(x.Choices,
					eval.Choice{Kind: eval.ChoiceOthers})
			case t.Kind == eval.KindRecord && c.Decl != nil &&
				c.Decl.Kind == DeclElement:
				x.Choices = append(x.Choices, eval.Choice{
					Kind: eval.ChoiceField, Field: c.Decl.Pos})
			case c.Kind == ExprRange:
				r, err := ev.rangeIR(c.Range, b)
				if err != nil {
					return nil, err
				}
				x.Choices = append(x.Choices, eval.Choice{
					Kind: eval.ChoiceRange, Range: r})
			default:
				ix, err := ev.lower(c, b)
				if err != nil {
					return nil, err
				}
				x.Choices = append(x.Choices, eval.Choice{
					Kind: eval.ChoiceIndex, Index: ix})
			}
		}
		ag.Assocs = append(ag.Assocs, x)
	}
	return ag, nil
}

// elemSubtype returns the subtype the value of an aggregate element
// association with choices cs belongs to: the element subtype of a
// one-dimensional array, the field's subtype of a record element named
// or placed by position alone; nil when there is no single one.
func elemSubtype(t *eval.Type, cs []*Expr) *eval.Type {
	switch t.Kind {
	case eval.KindArray:
		if len(t.BaseType().Index) != 1 {
			return nil
		}
		return t.Elem
	case eval.KindRecord:
		if len(cs) != 1 || cs[0].Decl == nil ||
			cs[0].Decl.Kind != DeclElement {
			return nil
		}
		if p := cs[0].Decl.Pos; p >= 0 && p < len(t.Fields) {
			return t.Fields[p].Type
		}
	}
	return nil
}

// layout evaluates the layout of t.
func (ev *Evaluator) layout(t *Type, b *eval.Budget) (*eval.Type, error) {
	if t == nil || t.Class == TypeUnknown {
		return nil, eval.NotStatic("an unknown type")
	}
	if t.Eval != nil {
		return ev.integerLayout(t.Eval), nil
	}
	if lt, ok := ev.sh.layouts[t]; ok {
		return lt, nil
	}
	if lt, ok := ev.layouts[t]; ok {
		ev.sh.reads++ // it depends on a binding
		return lt, nil
	}
	if ev.fr != nil {
		if lt, ok := ev.fr.layouts[t]; ok {
			ev.fr.loads++
			return lt, nil
		}
	}
	if err := b.Step(1); err != nil {
		return nil, err
	}
	if ev.busy[t] {
		return nil, eval.NotStatic("the recursive type " + t.Name())
	}
	if ev.busy == nil {
		ev.busy = map[*Type]bool{}
	}
	ev.busy[t] = true
	reads, loads, dyn := ev.sh.reads, ev.loads(), ev.sh.dynReads
	lt, err := ev.computeLayout(t, b)
	delete(ev.busy, t)
	frameFree := ev.fr == nil || ev.fr.loads == loads
	switch {
	case err != nil, ev.sh.dynReads != dyn:
	case ev.sh.reads == reads && frameFree:
		ev.sh.layouts[t] = lt
		ev.sh.fills++ // memo.go: a cold computation
	case frameFree:
		ev.layouts[t] = lt
		if ev.gen == nil { // an instance call's caches are its own
			ev.sh.fills++
		}
	default:
		if ev.fr.layouts == nil {
			ev.fr.layouts = map[*Type]*eval.Type{}
		}
		ev.fr.layouts[t] = lt
	}
	return lt, err
}

// integerLayout returns the layout of a std.standard type under ev's
// standard. The bundle is analyzed once for every standard, so its
// Type.Eval holds the 32-bit INTEGER, NATURAL and POSITIVE; under
// VHDL-2019 they are the 64-bit ones (LRM 5.2.3.2), and every type
// derived from them follows through computeLayout. STRING and
// BIT_VECTOR keep their index subtypes (eval.String).
func (ev *Evaluator) integerLayout(lt *eval.Type) *eval.Type {
	i64, n64, p64 := eval.StdIntegers(ev.std)
	switch lt {
	case eval.Integer:
		return i64
	case eval.Natural:
		return n64
	case eval.Positive:
		return p64
	}
	return lt
}

// openIndex reports whether an index constraint is open (LRM 5.3.2.2,
// arr(open)(...)): the index ranges stay those of the base, unknown
// until a later constraint or an object's value (InferSubtype) gives
// them, and only the element constraint applies.
func openIndex(cons []*RangeCons) bool {
	for _, rc := range cons {
		if rc != nil {
			return false
		}
	}
	return len(cons) > 0
}

func (ev *Evaluator) computeLayout(t *Type, b *eval.Budget,
) (*eval.Type, error) {
	if t.base != nil {
		base, err := ev.layout(t.base, b)
		if err != nil {
			return nil, err
		}
		out := *base
		out.Base = base.BaseType()
		if t.Decl != nil {
			out.Name = t.Decl.Text
		}
		if t.Range != nil {
			if base.Kind == eval.KindFloating {
				r, err := ev.realRangeOf(t.Range, b)
				if err != nil {
					return nil, err
				}
				if err := realCompatible(r, base); err != nil {
					return nil, err
				}
				out.Real = r
				return &out, nil
			}
			r, err := ev.rangeOf(t.Range, b)
			if err != nil {
				return nil, err
			}
			if err := compatible(r, base); err != nil {
				return nil, err
			}
			out.Range = r
		}
		if t.IndexCons != nil && !openIndex(t.IndexCons) {
			var bs []eval.Range
			for _, rc := range t.IndexCons {
				if rc == nil {
					return nil, eval.NotStatic("open index constraint")
				}
				r, err := ev.rangeOf(rc, b)
				if err != nil {
					return nil, err
				}
				if i := len(bs); i < len(base.Index) {
					if err := compatible(r, base.Index[i]); err != nil {
						return nil, err
					}
				}
				bs = append(bs, r)
			}
			out.Bounds = bs
		}
		if t.ElemSub != nil {
			el, err := ev.layout(t.ElemSub, b)
			if err != nil {
				return nil, err
			}
			out.Elem = el
		}
		return &out, nil
	}
	name := t.Name()
	switch t.Class {
	case TypeEnum:
		out := &eval.Type{Kind: eval.KindEnum, Name: name,
			Range: eval.Range{Left: 0, Right: int64(len(t.Literals)) - 1}}
		for _, l := range t.Literals {
			out.Literals = append(out.Literals, string(l.Name))
		}
		return out, nil
	case TypeInteger, TypePhysical:
		if t.Universal {
			return eval.UniversalInteger, nil
		}
		out := &eval.Type{Kind: eval.KindInteger, Name: name}
		if t.Class == TypePhysical {
			out.Kind = eval.KindPhysical
			for _, u := range t.Units {
				f, err := ev.unitFactor(u, b, 0)
				if err != nil {
					return nil, err
				}
				out.Units = append(out.Units, eval.Unit{
					Name: string(u.Name), Factor: f})
			}
		}
		if t.Range == nil {
			return nil, eval.NotStatic("the range of " + name)
		}
		r, err := ev.rangeOf(t.Range, b)
		if err != nil {
			return nil, err
		}
		out.Range = r
		return out, nil
	case TypeFloating:
		if t.Universal {
			return eval.UniversalReal, nil
		}
		out := &eval.Type{Kind: eval.KindFloating, Name: name,
			Real: eval.RealRange{Left: -math.MaxFloat64,
				Right: math.MaxFloat64}}
		if t.Range != nil {
			// type T is range L to R (LRM 5.2.5.1).
			r, err := ev.realRangeOf(t.Range, b)
			if err != nil {
				return nil, err
			}
			out.Real = r
		}
		return out, nil
	case TypeArray:
		out := &eval.Type{Kind: eval.KindArray, Name: name}
		for _, it := range t.Index {
			l, err := ev.layout(it, b)
			if err != nil {
				return nil, err
			}
			out.Index = append(out.Index, l)
		}
		el, err := ev.layout(t.Elem, b)
		if err != nil {
			return nil, err
		}
		out.Elem = el
		return out, nil
	case TypeRecord:
		out := &eval.Type{Kind: eval.KindRecord, Name: name}
		for _, f := range t.Fields {
			l, err := ev.layout(f.Type, b)
			if err != nil {
				return nil, err
			}
			out.Fields = append(out.Fields, eval.Field{
				Name: string(f.Name), Type: l})
		}
		return out, nil
	case TypeAccess:
		return &eval.Type{Kind: eval.KindAccess, Name: name}, nil
	case TypeFile:
		return &eval.Type{Kind: eval.KindFile, Name: name}, nil
	case TypeProtected:
		return &eval.Type{Kind: eval.KindProtected, Name: name}, nil
	case TypeGeneric:
		ev.sh.reads++
		if t.Decl != nil {
			if bt, ok := ev.types[t.Decl]; ok {
				return ev.layout(bt, b)
			}
			if lt, ok := ev.genLayout(t.Decl); ok {
				return lt, nil
			}
		}
	}
	return nil, eval.NotStatic("the type " + name)
}

// compatible checks that the range r of a range or index constraint is
// compatible with the subtype t it constrains: a null range always is,
// otherwise both bounds belong to t (LRM 5.2.1, 5.3.2.2).
func compatible(r eval.Range, t *eval.Type) error {
	if t == nil || r.Null() {
		return nil
	}
	switch t.Kind {
	case eval.KindInteger, eval.KindEnum, eval.KindPhysical:
	default:
		return nil
	}
	for _, v := range []int64{r.Left, r.Right} {
		if !t.Range.Contains(v) {
			return eval.RangeError(eval.Value{T: t, I: v}, t)
		}
	}
	return nil
}

// staticChecks evaluates the constants of every project unit that need
// no instance and reports values out of range, bad indices and width
// mismatches (sema/static-error) and the step limit (sema/eval-limit).
// It returns the incomplete entries.
func (s *session) staticChecks() []string {
	if s.base == nil {
		return nil
	}
	libs := func(k Key) (*Library, bool) {
		l, ok := s.libs[k]
		return l, ok
	}
	var incomplete []string
	base := newEvaluator(libs, s.steps)
	base.std = s.std
	for _, u := range s.units {
		if u.Opaque || u.Unchecked {
			continue
		}
		ev := base.Fresh()
		for _, d := range u.Decls {
			if d.Implicit || d.File != u.File || inSubprogram(d) {
				continue
			}
			var err error
			rg := d.Full
			switch {
			case d.Kind == DeclConstant && d.Value != nil:
				_, err = ev.constant(d, eval.NewBudget(s.steps))
				rg = d.Value.Node.Span()
			case d.Kind == DeclSignal || d.Kind == DeclSharedVariable:
				rg, err = ev.signalCheck(d, eval.NewBudget(s.steps))
			case d.Kind == DeclType || d.Kind == DeclSubtype:
				// A range or index constraint incompatible with the
				// subtype it constrains (LRM 5.2.1, 5.3.2.2).
				_, err = ev.layout(d.Type, eval.NewBudget(s.steps))
				err = designOnly(err)
			case d.Kind == DeclVariable && inProcess(d):
				// A process variable is elaborated once, with its
				// unit: its subtype and initial value are static
				// checks like a signal's (LRM 14.4.2.5).
				rg, err = ev.signalCheck(d, eval.NewBudget(s.steps))
				err = designOnly(err)
			default:
				continue
			}
			if err == nil {
				continue
			}
			switch c := eval.CodeOf(err); {
			case c == eval.ErrCodeBudget:
				u.diags = append(u.diags, at(u.File, rg,
					diag.New(diag.SemaEvalLimit, d.Kind.String()+" "+
						d.Text)))
				incomplete = append(incomplete, u.Label())
			case DesignError(c):
				u.diags = append(u.diags, at(u.File, rg,
					diag.New(diag.SemaStaticError, err.Error(),
						u.Label())))
			}
		}
		u.diags = append(u.diags, targetChecks(u)...)
		for _, tok := range bigLiterals(u.Node) {
			u.diags = append(u.diags, at(u.File, tok.Span(),
				diag.New(diag.SemaStaticError, "integer literal "+
					tok.Text()+" is outside the range of "+
					"universal_integer", u.Label())))
		}
		ev.processChecks(u.Body, s.steps, func(n cst.Node, err error) {
			u.diags = append(u.diags, at(u.File, n.Span(),
				diag.New(diag.SemaStaticError, err.Error(),
					u.Label())))
		})
		if s.assignChecks(u, ev) {
			incomplete = append(incomplete, u.Label())
		}
		if s.packageInstChecks(u, base) {
			incomplete = append(incomplete, u.Label())
		}
	}
	return incomplete
}

// packageInstChecks evaluates, for each package instance of unit u
// whose generic map gives generic types, the constants of the
// uninstantiated package and of its body with those types bound (LRM
// 4.9: the instance elaborates a copy of both). An error that depends
// on the actual types is reported at the constant, naming the
// instance; one that does not is the package's own (staticChecks). It
// reports whether one hit the step limit.
func (s *session) packageInstChecks(u *Unit, base *Evaluator) bool {
	insts := []*Decl{}
	if u.Decl != nil && u.Decl.Kind == DeclPackageInst {
		insts = append(insts, u.Decl)
	}
	for _, d := range u.Decls {
		if d.Kind == DeclPackageInst && d != u.Decl && !inSubprogram(d) {
			insts = append(insts, d)
		}
	}
	limited := false
	for _, d := range insts {
		x := d.Target
		if x == nil || len(d.instTypes) == 0 || x.Unit == nil ||
			x.Unit.Decl != x || x.Region == nil {
			continue
		}
		ev := base.Fresh()
		for _, g := range x.Region.Decls {
			if g.Kind != DeclGenericType || g.Type == nil {
				continue
			}
			if t, ok := d.instTypes[g.Type]; ok && !t.Unknown() {
				ev.bindType(g, t)
			}
		}
		label := u.Label()
		if d != u.Decl {
			label += "." + d.Text
		}
		units := []*Unit{x.Unit}
		for _, b := range s.libs[x.Unit.Library].Secondary(x.Unit.Name) {
			if b.Kind == UnitPackageBody && !b.Duplicate {
				units = append(units, b)
			}
		}
		for _, pu := range units {
			if pu.Opaque || pu.Unchecked {
				continue
			}
			for _, c := range pu.Decls {
				if c.Kind != DeclConstant || c.Value == nil ||
					c.Implicit || c.File != pu.File || inSubprogram(c) {
					continue
				}
				var err error
				if !ev.Track(func() {
					_, err = ev.constant(c, eval.NewBudget(s.steps))
				}) || err == nil {
					continue
				}
				rg := c.Value.Node.Span()
				switch code := eval.CodeOf(err); {
				case code == eval.ErrCodeBudget:
					limited = true
					u.diags = append(u.diags, at(pu.File, rg,
						diag.New(diag.SemaEvalLimit, "constant "+c.Text+
							" of "+label)))
				case DesignError(code):
					u.diags = append(u.diags, at(pu.File, rg,
						diag.New(diag.SemaStaticError, "constant "+c.Text+
							": "+err.Error(), label)))
				}
			}
		}
	}
	return limited
}

// assignChecks checks the assignments of unit u's bodies (Assign) with
// the unit's evaluator and reports whether one hit the step limit.
func (s *session) assignChecks(u *Unit, ev *Evaluator) bool {
	limited := false
	var walk func(r *Region)
	walk = func(r *Region) {
		if r == nil || r.Unit != u {
			return
		}
		if r.Kind == RegionGenerate {
			// Only an elaborated alternative or iteration exists: the
			// instance hierarchy checks it (internal/elab/hier).
			return
		}
		for _, as := range r.Assigns {
			n, err := ev.checkAssign(as, eval.NewBudget(s.steps))
			if err == nil {
				continue
			}
			switch c := eval.CodeOf(err); {
			case c == eval.ErrCodeBudget:
				limited = true
				u.diags = append(u.diags, at(u.File, n.Span(),
					diag.New(diag.SemaEvalLimit, "assignment to "+
						u.File.text(as.Target.Node))))
			case DesignError(c):
				u.diags = append(u.diags, at(u.File, n.Span(),
					diag.New(diag.SemaStaticError, "assignment to "+
						u.File.text(as.Target.Node)+": "+err.Error(),
						u.Label())))
			}
		}
		for _, c := range r.Children {
			walk(c)
		}
	}
	walk(u.Region)
	return limited
}

// signalCheck evaluates the subtype and the initial value of a signal
// or shared variable, returning where an error is.
func (ev *Evaluator) signalCheck(d *Decl, b *eval.Budget,
) (cst.Range, error) {
	t, err := ev.layout(d.Type, b)
	if err != nil || d.Value == nil {
		return d.Full, err
	}
	v, err := ev.value(d.Value, b)
	if err == nil {
		_, err = eval.Check(v, t)
	}
	return d.Value.Node.Span(), err
}

// bigLiterals returns the integer literals of design unit n whose value
// lies outside the implementation's universal_integer, a 64-bit
// two's-complement range as in GHDL and NVC (LRM 5.2.3.1, 15.5.2).
func bigLiterals(n cst.Node) []cst.Token {
	var out []cst.Token
	for tok := range n.DescendantTokens() {
		if tok.Kind() != token.IntegerLiteral || tok.Missing() ||
			tok.Inactive() {
			continue
		}
		if _, ok := ParseInteger(tok.Text()); !ok && wellFormed(tok.Text()) {
			out = append(out, tok)
		}
	}
	return out
}

// wellFormed reports whether integer literal s has only valid digits for
// its base, so that ParseInteger failing on it means overflow.
func wellFormed(s string) bool {
	s = strings.ReplaceAll(lowerASCII(s), "_", "")
	mant, base := s, 10
	if i := strings.IndexAny(s, "#:"); i >= 0 {
		b, err := strconv.Atoi(s[:i])
		if err != nil || b < 2 || b > 16 {
			return false
		}
		rest := s[i+1:]
		j := strings.IndexAny(rest, "#:")
		if j < 0 {
			return false
		}
		mant, base = rest[:j], b
		s = rest[j+1:]
	} else if j := strings.IndexByte(s, 'e'); j >= 0 {
		mant, s = s[:j], s[j:]
	} else {
		s = ""
	}
	if mant == "" {
		return false
	}
	for _, c := range mant {
		if d := strings.IndexRune("0123456789abcdef", c); d < 0 ||
			d >= base {
			return false
		}
	}
	if s == "" {
		return true
	}
	exp := strings.TrimPrefix(strings.TrimPrefix(s, "e"), "+")
	if exp == "" {
		return false
	}
	for _, c := range exp {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// designOnly keeps only a design error: the checks added for types and
// process variables never report the step limit.
func designOnly(err error) error {
	if DesignError(eval.CodeOf(err)) {
		return err
	}
	return nil
}

// inProcess reports whether d is declared in a process (outside any
// subprogram).
func inProcess(d *Decl) bool {
	for r := d.Parent; r != nil; r = r.Parent {
		switch r.Kind {
		case RegionSubprogram:
			return false
		case RegionProcess:
			return true
		}
	}
	return false
}

// inSubprogram reports whether d is declared in a subprogram body:
// such a constant is elaborated by each call, not with its unit.
func inSubprogram(d *Decl) bool {
	for r := d.Parent; r != nil; r = r.Parent {
		if r.Kind == RegionSubprogram {
			return true
		}
	}
	return false
}

// DesignError reports whether an evaluation error is the design's
// (a value out of range, a bad index, a width mismatch, division by
// zero, overflow) rather than a limit of static evaluation.
func DesignError(c eval.Code) bool {
	switch c {
	case eval.ErrCodeRange, eval.ErrCodeIndex, eval.ErrCodeLength,
		eval.ErrCodeDivZero, eval.ErrCodeOverflow:
		return true
	}
	return false
}

// ParseInteger parses a VHDL integer literal: decimal or based, with
// underscores and a non-negative exponent (LRM 15.5).
func ParseInteger(s string) (int64, bool) {
	s = strings.ReplaceAll(lowerASCII(s), "_", "")
	base := int64(10)
	mant, exp := s, ""
	if i := strings.IndexAny(s, "#:"); i >= 0 {
		b, err := strconv.ParseInt(s[:i], 10, 64)
		if err != nil || b < 2 || b > 16 {
			return 0, false
		}
		base = b
		rest := s[i+1:]
		j := strings.IndexAny(rest, "#:")
		if j < 0 {
			return 0, false
		}
		mant, exp = rest[:j], strings.TrimPrefix(rest[j+1:], "e")
	} else if i := strings.IndexByte(s, 'e'); i >= 0 {
		mant, exp = s[:i], s[i+1:]
	}
	v, err := strconv.ParseInt(mant, int(base), 64)
	if err != nil {
		return 0, false
	}
	if exp != "" {
		e, err := strconv.ParseInt(strings.TrimPrefix(exp, "+"), 10, 64)
		if err != nil || e < 0 {
			return 0, false
		}
		if v == 0 {
			return 0, true // 0E1000000000 needs no loop
		}
		for range e {
			var ok bool
			if v, ok = mulOK(v, base); !ok {
				return 0, false
			}
		}
	}
	return v, true
}

func mulOK(a, b int64) (int64, bool) {
	if a == 0 {
		return 0, true
	}
	c := a * b
	if c/b != a {
		return 0, false
	}
	return c, true
}

// ParseReal parses a VHDL real literal (decimal or based).
func ParseReal(s string) (float64, bool) {
	s = strings.ReplaceAll(lowerASCII(s), "_", "")
	if i := strings.IndexAny(s, "#:"); i >= 0 {
		b, err := strconv.ParseInt(s[:i], 10, 64)
		if err != nil || b < 2 || b > 16 {
			return 0, false
		}
		rest := s[i+1:]
		j := strings.IndexAny(rest, "#:")
		if j < 0 {
			return 0, false
		}
		mant, exp := rest[:j], strings.TrimPrefix(rest[j+1:], "e")
		ip, fp, _ := strings.Cut(mant, ".")
		v := 0.0
		for _, c := range ip + fp {
			d := strings.IndexRune("0123456789abcdef", c)
			if d < 0 || int64(d) >= b {
				return 0, false
			}
			v = v*float64(b) + float64(d)
		}
		v /= math.Pow(float64(b), float64(len(fp)))
		if exp != "" {
			e, err := strconv.ParseInt(strings.TrimPrefix(exp, "+"), 10,
				64)
			if err != nil {
				return 0, false
			}
			v *= math.Pow(float64(b), float64(e))
		}
		return v, true
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// maxBits bounds the expansion of a bit-string literal whose size
// prefix asks for more (8000000000X"0"): it is not evaluated, and the
// expansion never allocates more than this (P7).
const maxBits = 1 << 22

// ExpandBitString returns the characters of a bit-string literal
// (LRM 15.8): B, O, X, D with the VHDL-2008 U and S forms and an
// optional size. It fails for malformed literals and for expansions
// longer than limit characters.
func ExpandBitString(s string, limit int) (string, bool) {
	q := strings.IndexByte(s, '"')
	if q < 0 || !strings.HasSuffix(s, `"`) || len(s) < q+2 {
		return "", false
	}
	prefix := lowerASCII(s[:q])
	digits := strings.ReplaceAll(s[q+1:len(s)-1], "_", "")
	size := -1
	i := 0
	for i < len(prefix) && prefix[i] >= '0' && prefix[i] <= '9' {
		i++
	}
	if i > 0 {
		n, err := strconv.Atoi(prefix[:i])
		if err != nil || n > limit {
			return "", false
		}
		size = n
	}
	if len(digits)*4 > limit {
		return "", false
	}
	base := prefix[i:]
	signed := strings.HasPrefix(base, "s")
	base = strings.TrimLeft(base, "us")
	var bits strings.Builder
	switch base {
	case "b", "o", "x":
		w := map[string]int{"b": 1, "o": 3, "x": 4}[base]
		for j := range len(digits) {
			c := digits[j]
			v := strings.IndexByte("0123456789abcdef", lowerByte(c))
			if v < 0 || v >= 1<<w {
				// A graphic character other than a digit repeats
				// (VHDL-2008).
				bits.WriteString(strings.Repeat(string(c), w))
				continue
			}
			for k := w - 1; k >= 0; k-- {
				bits.WriteByte(byte('0' + (v>>k)&1))
			}
		}
	case "d":
		n, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			return "", false
		}
		bits.WriteString(strconv.FormatUint(n, 2))
		if n == 0 {
			bits.Reset()
			bits.WriteByte('0')
		}
	default:
		return "", false
	}
	out := bits.String()
	if size < 0 {
		return out, true
	}
	switch {
	case size > len(out):
		pad := byte('0')
		if signed && len(out) > 0 {
			pad = out[0]
		}
		out = strings.Repeat(string(pad), size-len(out)) + out
	case size < len(out):
		out = out[len(out)-size:]
	}
	return out, true
}

func lowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// text returns the source text of n in f without its trivia.
func (f *File) text(n cst.Node) string {
	sp := n.Span()
	if sp.Start < 0 || sp.End > len(f.Text) || sp.Start > sp.End {
		return n.Text()
	}
	return string(f.Text[sp.Start:sp.End])
}
