package eval

import (
	"math"
	"strconv"
)

// Ref identifies an object (constant, generic, signal, variable) for
// Env.Load; Func identifies a user subprogram for Env.Call. Both are
// opaque to the evaluator: sema passes its declarations, the simulator
// its own handles.
type (
	Ref  any
	Func any
)

// Env is how an evaluation reaches the world outside the expression.
// Implementations must be pure for a given state: the static evaluator
// returns constants and generics and NotStatic for signals and
// variables; the simulator returns current values.
type Env interface {
	// Load returns the value of the object ref.
	Load(ref Ref) (Value, error)
	// Call evaluates the user function fn on args, charging its own
	// steps to b.
	Call(fn Func, args []Value, b *Budget) (Value, error)
}

// Expr is a node of the evaluator IR. Sema lowers its typed
// expressions to Exprs; the set of node types is closed (the unexported
// method), so the evaluator is the one place that defines what they
// mean.
type Expr interface {
	eval(m *Machine) (Value, error)
}

// Const is a literal or folded value.
type Const struct{ V Value }

// Load reads an object through Env.Load and checks it against T.
type Load struct {
	Ref Ref
	T   *Type
}

// Operation is a predefined operation on one or two operands. The
// logical operators on BIT and BOOLEAN scalars short-circuit (LRM
// 9.2.2).
type Operation struct {
	Op   Op
	Args []Expr
	T    *Type
}

// Call calls a user function through Env.Call.
type Call struct {
	Fn   Func
	Args []Expr
	T    *Type
}

// Index is an indexed name X(i, j, ...).
type Index struct {
	X   Expr
	Idx []Expr
	T   *Type
}

// Slice is a slice X(R) of a one-dimensional array.
type Slice struct {
	X Expr
	R RangeExpr
	T *Type
}

// Element selects record element N.
type Element struct {
	X Expr
	N int
	T *Type
}

// Convert is a type conversion to T (LRM 9.3.6): between numeric
// types (real to integer rounds to nearest, ties away from zero) and
// between closely related array types.
type Convert struct {
	X Expr
	T *Type
}

// Qualify is a qualified expression T'(X): X checked against T.
type Qualify struct {
	X Expr
	T *Type
}

// Cond is the VHDL-2019 conditional expression: the value of the first
// branch whose condition is true, else Else.
type Cond struct {
	Conds  []Expr
	Values []Expr
	Else   Expr
}

// RangeExpr is a range: Left Dir Right, or the range of an array
// value's dimension (Of with Dim, for X'RANGE) or of a scalar subtype
// (Type, for T'RANGE). Reverse gives 'REVERSE_RANGE.
type RangeExpr struct {
	Left, Right Expr
	Dir         Dir
	Of          Expr
	Type        *Type
	Dim         int // zero-based dimension for Of
	Reverse     bool
}

// ChoiceKind is the kind of an aggregate choice.
type ChoiceKind uint8

// Aggregate choice kinds.
const (
	ChoicePositional ChoiceKind = iota
	ChoiceIndex                 // an index value expression
	ChoiceRange                 // a discrete range
	ChoiceOthers
	ChoiceField // a record element, by position N
)

// Choice is one choice of an aggregate element association.
type Choice struct {
	Kind  ChoiceKind
	Index Expr
	Range RangeExpr
	Field int
}

// Assoc is one element association: choices => Value. A positional
// association has one ChoicePositional choice.
type Assoc struct {
	Choices []Choice
	Value   Expr
}

// Aggregate is an array or record aggregate of type T (LRM 9.3.3).
// Array aggregates are one-dimensional; a multi-dimensional aggregate
// is an aggregate of aggregates whose values sema types as rows.
type Aggregate struct {
	T      *Type
	Assocs []Assoc
}

// AttrKind is a predefined value attribute (LRM 16.2).
type AttrKind uint8

// Value attributes.
const (
	AttrLeft AttrKind = iota + 1
	AttrRight
	AttrHigh
	AttrLow
	AttrLength
	AttrAscending
	AttrPos
	AttrVal
	AttrSucc
	AttrPred
	AttrLeftof
	AttrRightof
	AttrImage
	AttrValue
)

var attrNames = [...]string{
	AttrLeft: "left", AttrRight: "right", AttrHigh: "high",
	AttrLow: "low", AttrLength: "length", AttrAscending: "ascending",
	AttrPos: "pos", AttrVal: "val", AttrSucc: "succ", AttrPred: "pred",
	AttrLeftof: "leftof", AttrRightof: "rightof", AttrImage: "image",
	AttrValue: "value",
}

// String returns the attribute designator in lower case.
func (a AttrKind) String() string {
	if int(a) < len(attrNames) && attrNames[a] != "" {
		return attrNames[a]
	}
	return "AttrKind(" + strconv.Itoa(int(a)) + ")"
}

// LookupAttr returns the value attribute named name (lower case).
func LookupAttr(name string) (AttrKind, bool) {
	for i, n := range attrNames {
		if n != "" && n == name {
			return AttrKind(i), true
		}
	}
	return 0, false
}

// Attr is a predefined value attribute of a scalar subtype (Type) or of
// an array value (Of, dimension Dim, zero-based). Arg is the parameter
// of 'POS, 'VAL, 'SUCC, 'IMAGE, ... T is the result type.
type Attr struct {
	Kind AttrKind
	Type *Type
	Of   Expr
	Dim  int
	Arg  Expr
	T    *Type
}

// Machine evaluates IR expressions against an Env under a Budget.
type Machine struct {
	Env    Env
	Budget *Budget
}

// Eval evaluates e. Every node costs one step.
func (m *Machine) Eval(e Expr) (Value, error) {
	if e == nil {
		return Value{}, errf(ErrCodeType, "missing expression")
	}
	if err := m.Budget.Step(1); err != nil {
		return Value{}, err
	}
	return e.eval(m)
}

func (c *Const) eval(*Machine) (Value, error) { return c.V, nil }

func (l *Load) eval(m *Machine) (Value, error) {
	if m.Env == nil {
		return Value{}, NotStatic("object")
	}
	v, err := m.Env.Load(l.Ref)
	if err != nil {
		return Value{}, err
	}
	return Check(v, l.T)
}

func (o *Operation) eval(m *Machine) (Value, error) {
	if len(o.Args) == 2 && shortCircuit(o.Op) {
		x, err := m.Eval(o.Args[0])
		if err != nil {
			return Value{}, err
		}
		if x.Kind() == KindEnum {
			// and/nand stop on '0', or/nor stop on '1'.
			stop := o.Op == OpOr || o.Op == OpNor
			if (x.I != 0) == stop {
				v := x.I
				if o.Op == OpNand || o.Op == OpNor {
					v = 1 - v
				}
				return Value{T: o.T, I: v}, nil
			}
		}
		y, err := m.Eval(o.Args[1])
		if err != nil {
			return Value{}, err
		}
		return Apply(m.Budget, o.Op, o.T, x, y)
	}
	args := make([]Value, len(o.Args))
	for i, a := range o.Args {
		v, err := m.Eval(a)
		if err != nil {
			return Value{}, err
		}
		args[i] = v
	}
	return Apply(m.Budget, o.Op, o.T, args...)
}

func shortCircuit(o Op) bool {
	return o == OpAnd || o == OpOr || o == OpNand || o == OpNor
}

func (c *Call) eval(m *Machine) (Value, error) {
	args := make([]Value, len(c.Args))
	for i, a := range c.Args {
		v, err := m.Eval(a)
		if err != nil {
			return Value{}, err
		}
		args[i] = v
	}
	if m.Env == nil {
		return Value{}, NotStatic("function call")
	}
	v, err := m.Env.Call(c.Fn, args, m.Budget)
	if err != nil {
		return Value{}, err
	}
	return Check(v, c.T)
}

func (x *Index) eval(m *Machine) (Value, error) {
	a, err := m.Eval(x.X)
	if err != nil {
		return Value{}, err
	}
	if a.Kind() != KindArray {
		return Value{}, errf(ErrCodeType, "indexing a "+
			a.Kind().String()+" value")
	}
	off := int64(0)
	for d, ie := range x.Idx {
		iv, err := m.Eval(ie)
		if err != nil {
			return Value{}, err
		}
		r := dim(a, d)
		if !r.Contains(iv.I) {
			return Value{}, IndexError(iv.I, r)
		}
		off = off*r.Len() + r.Offset(iv.I)
	}
	if off < 0 || off >= int64(len(a.E)) {
		return Value{}, errf(ErrCodeIndex, "index out of range")
	}
	return a.E[off], nil
}

// dim returns the index range of dimension d of the array value a.
func dim(a Value, d int) Range {
	if d < len(a.B) {
		return a.B[d]
	}
	return Range{Left: 0, Right: int64(len(a.E)) - 1}
}

func (s *Slice) eval(m *Machine) (Value, error) {
	a, err := m.Eval(s.X)
	if err != nil {
		return Value{}, err
	}
	if a.Kind() != KindArray {
		return Value{}, errf(ErrCodeType, "slicing a "+
			a.Kind().String()+" value")
	}
	r, err := m.evalRange(s.R)
	if err != nil {
		return Value{}, err
	}
	ar := dim(a, 0)
	if r.Null() {
		return Value{T: a.T, B: []Range{r}}, nil
	}
	if r.Dir != ar.Dir || !ar.Contains(r.Left) || !ar.Contains(r.Right) {
		return Value{}, SliceError(r, ar)
	}
	if err := m.Budget.Step(r.Len()); err != nil {
		return Value{}, err
	}
	lo := ar.Offset(r.Left)
	out := make([]Value, r.Len())
	copy(out, a.E[lo:lo+r.Len()])
	t := a.T
	if s.T != nil {
		t = s.T
	}
	return Value{T: t, B: []Range{r}, E: out}, nil
}

func (f *Element) eval(m *Machine) (Value, error) {
	r, err := m.Eval(f.X)
	if err != nil {
		return Value{}, err
	}
	if r.Kind() != KindRecord || f.N < 0 || f.N >= len(r.E) {
		return Value{}, errf(ErrCodeType, "selecting a field of a "+
			r.Kind().String()+" value")
	}
	return r.E[f.N], nil
}

func (q *Qualify) eval(m *Machine) (Value, error) {
	v, err := m.Eval(q.X)
	if err != nil {
		return Value{}, err
	}
	return Check(v, q.T)
}

func (c *Convert) eval(m *Machine) (Value, error) {
	v, err := m.Eval(c.X)
	if err != nil {
		return Value{}, err
	}
	t := c.T
	switch {
	case t.Kind == KindInteger && v.Kind() == KindFloating:
		r := math.Round(v.F) // ties away from zero (LRM 9.3.6)
		if r >= math.MaxInt64 || r < math.MinInt64 || math.IsNaN(r) {
			return Value{}, RangeError(v, t)
		}
		return Check(Value{T: t, I: int64(r)}, t)
	case t.Kind == KindFloating && v.Kind() == KindInteger:
		return Check(Value{T: t, F: float64(v.I)}, t)
	case t.Kind == KindArray && v.Kind() == KindArray:
		if err := m.Budget.Step(int64(len(v.E))); err != nil {
			return Value{}, err
		}
		out := v
		out.T = t
		if t.Bounds == nil && len(v.B) == 1 {
			// The result takes the index subtype's direction and left
			// bound when the index types differ; keep the operand's
			// bounds otherwise (LRM 9.3.6).
			out.B = v.B
		}
		return Check(out, t)
	}
	return Check(v, t)
}

func (c *Cond) eval(m *Machine) (Value, error) {
	for i, ce := range c.Conds {
		b, err := m.Eval(ce)
		if err != nil {
			return Value{}, err
		}
		if b.I != 0 {
			return m.Eval(c.Values[i])
		}
	}
	return m.Eval(c.Else)
}

// EvalRange evaluates a range.
func (m *Machine) EvalRange(r RangeExpr) (Range, error) {
	if err := m.Budget.Step(1); err != nil {
		return Range{}, err
	}
	return m.evalRange(r)
}

func (m *Machine) evalRange(r RangeExpr) (Range, error) {
	var out Range
	switch {
	case r.Type != nil:
		out = r.Type.Range
	case r.Of != nil:
		a, err := m.Eval(r.Of)
		if err != nil {
			return Range{}, err
		}
		out = dim(a, r.Dim)
	default:
		l, err := m.Eval(r.Left)
		if err != nil {
			return Range{}, err
		}
		h, err := m.Eval(r.Right)
		if err != nil {
			return Range{}, err
		}
		out = Range{Left: l.I, Right: h.I, Dir: r.Dir}
	}
	if r.Reverse {
		out = Range{Left: out.Right, Right: out.Left, Dir: 1 - out.Dir}
	}
	return out, nil
}

func (a *Aggregate) eval(m *Machine) (Value, error) {
	if a.T == nil {
		return Value{}, errf(ErrCodeType, "aggregate without a type")
	}
	if a.T.Kind == KindRecord {
		return m.recordAggregate(a)
	}
	if a.T.Kind != KindArray {
		return Value{}, errf(ErrCodeType, "aggregate of a "+
			a.T.Kind.String()+" type")
	}
	return m.arrayAggregate(a)
}

func (m *Machine) recordAggregate(a *Aggregate) (Value, error) {
	fs := a.T.BaseType().Fields
	out := make([]Value, len(fs))
	set := make([]bool, len(fs))
	pos := 0
	for _, as := range a.Assocs {
		v, err := m.Eval(as.Value)
		if err != nil {
			return Value{}, err
		}
		for _, c := range as.Choices {
			switch c.Kind {
			case ChoicePositional:
				if pos < len(out) {
					out[pos], set[pos] = v, true
				}
				pos++
			case ChoiceField:
				if c.Field >= 0 && c.Field < len(out) {
					out[c.Field], set[c.Field] = v, true
				}
			case ChoiceOthers:
				for i := range out {
					if !set[i] {
						out[i], set[i] = v, true
					}
				}
			}
		}
	}
	for i, ok := range set {
		if !ok {
			return Value{}, errf(ErrCodeType, "no value for field "+
				fs[i].Name)
		}
	}
	return Value{T: a.T, E: out}, nil
}

func (m *Machine) arrayAggregate(a *Aggregate) (Value, error) {
	t := a.T
	idx := Range{}
	if bt := t.BaseType(); len(bt.Index) > 0 {
		idx = Range{Left: bt.Index[0].Range.Left,
			Dir: bt.Index[0].Range.Dir}
	}
	if len(t.BaseType().Index) > 1 && t.Bounds == nil {
		// A row of an unconstrained multi-dimensional aggregate has no
		// applicable index constraint, so it may not use others (LRM
		// 9.3.3.3).
		for _, as := range a.Assocs {
			if row, ok := as.Value.(*Aggregate); ok &&
				row.T != nil && row.T.Bounds == nil && hasOthers(row) {
				return Value{}, errf(ErrCodeLength, "an others choice "+
					"in a row of an unconstrained aggregate")
			}
		}
	}
	type item struct {
		lo, hi int64 // index span, or positional slot
		v      Value
		pos    bool
		others bool
	}
	var items []item
	npos := int64(0)
	named := false
	for _, as := range a.Assocs {
		v, err := m.Eval(as.Value)
		if err != nil {
			return Value{}, err
		}
		for _, c := range as.Choices {
			switch c.Kind {
			case ChoicePositional:
				items = append(items, item{lo: npos, hi: npos, v: v,
					pos: true})
				npos++
			case ChoiceIndex:
				iv, err := m.Eval(c.Index)
				if err != nil {
					return Value{}, err
				}
				items = append(items, item{lo: iv.I, hi: iv.I, v: v})
				named = true
			case ChoiceRange:
				r, err := m.evalRange(c.Range)
				if err != nil {
					return Value{}, err
				}
				if !r.Null() {
					items = append(items, item{lo: r.Low(), hi: r.High(),
						v: v})
				}
				named = true
			case ChoiceOthers:
				items = append(items, item{v: v, others: true})
			}
		}
	}
	// The index range of the result (LRM 9.3.3.3): an aggregate with
	// others takes the context's bounds, one with named choices only
	// the bounds of its choices (converted to a constrained context
	// afterwards), a positional one the context's or the index
	// subtype's left bound.
	others := false
	for _, it := range items {
		others = others || it.others
	}
	var r Range
	switch {
	case t.Bounds != nil && (others || !named):
		r = t.Bounds[0]
	case others:
		return Value{}, NotStatic("an aggregate with others " +
			"without a constrained context")
	case named:
		lo, hi := int64(math.MaxInt64), int64(math.MinInt64)
		for _, it := range items {
			if !it.pos && !it.others {
				lo, hi = min(lo, it.lo), max(hi, it.hi)
			}
		}
		if idx.Dir == Downto {
			r = Range{Left: hi, Right: lo, Dir: Downto}
		} else {
			r = Range{Left: lo, Right: hi, Dir: To}
		}
	default:
		r = Range{Left: idx.Left, Dir: idx.Dir}
		r.Right = r.At(npos - 1)
	}
	n := r.Len()
	if err := m.Budget.Step(n); err != nil {
		return Value{}, err
	}
	if n > 1<<26 {
		return Value{}, Unsupported("an aggregate of " +
			strconv.FormatInt(n, 10) + " elements")
	}
	out := make([]Value, n)
	set := make([]bool, n)
	for _, it := range items {
		switch {
		case it.pos:
			if it.lo >= n {
				return Value{}, LengthError(n, npos)
			}
			out[it.lo], set[it.lo] = it.v, true
		case it.others:
			for i := range out {
				if !set[i] {
					out[i], set[i] = it.v, true
				}
			}
		default:
			for i := it.lo; i <= it.hi; i++ {
				if !r.Contains(i) {
					return Value{}, IndexError(i, r)
				}
				o := r.Offset(i)
				out[o], set[o] = it.v, true
			}
		}
	}
	for i, ok := range set {
		if !ok {
			return Value{}, errf(ErrCodeLength, "no value for index "+
				strconv.FormatInt(r.At(int64(i)), 10))
		}
	}
	if len(t.BaseType().Index) > 1 {
		return flatten(t, r, out)
	}
	v := Value{T: t, B: []Range{r}, E: out}
	if t.Bounds != nil && r != t.Bounds[0] {
		return Check(v, t)
	}
	return v, nil
}

func hasOthers(a *Aggregate) bool {
	for _, as := range a.Assocs {
		for _, c := range as.Choices {
			if c.Kind == ChoiceOthers {
				return true
			}
		}
	}
	return false
}

// flatten turns the rows of a multi-dimensional aggregate (each an
// array over the remaining dimensions) into one row-major value. Every
// row must have the same shape.
func flatten(t *Type, r Range, rows []Value) (Value, error) {
	v := Value{T: t, B: []Range{r}}
	if len(rows) == 0 {
		// A null aggregate: the other dimensions come from the
		// context, or are null.
		for d := 1; d < len(t.BaseType().Index); d++ {
			if d < len(t.Bounds) {
				v.B = append(v.B, t.Bounds[d])
			} else {
				v.B = append(v.B, Range{Left: 0, Right: -1})
			}
		}
		return v, nil
	}
	for i, row := range rows {
		if i == 0 {
			v.B = append(v.B, row.B...)
		} else if len(row.E) != len(rows[0].E) {
			return Value{}, LengthError(int64(len(rows[0].E)),
				int64(len(row.E)))
		}
		v.E = append(v.E, row.E...)
	}
	return v, nil
}

func (a *Attr) eval(m *Machine) (Value, error) {
	var arg Value
	if a.Arg != nil {
		v, err := m.Eval(a.Arg)
		if err != nil {
			return Value{}, err
		}
		arg = v
	}
	if a.Of != nil {
		v, err := m.Eval(a.Of)
		if err != nil {
			return Value{}, err
		}
		if a.Kind == AttrImage && a.Arg == nil {
			// O'IMAGE of an object prefix (VHDL-2019).
			return imageAttr(m, v)
		}
		return arrayAttr(a, dim(v, a.Dim))
	}
	t := a.Type
	if t == nil {
		return Value{}, errf(ErrCodeType, "'"+a.Kind.String()+
			" without a prefix")
	}
	if a.Kind == AttrImage &&
		(t.Kind == KindArray || t.Kind == KindRecord) {
		// T'IMAGE of a composite type (VHDL-2019) also takes an
		// unconstrained array type: the bounds come from the value.
		return imageAttr(m, arg)
	}
	if t.Kind == KindArray {
		if t.Bounds == nil || a.Dim >= len(t.Bounds) {
			return Value{}, NotStatic("'" + a.Kind.String() +
				" of an unconstrained array type")
		}
		return arrayAttr(a, t.Bounds[a.Dim])
	}
	return scalarAttr(a, t, arg)
}

// imageAttr is 'IMAGE of any value: one step per scalar written, so
// the image of a large composite is bounded by the budget.
func imageAttr(m *Machine, v Value) (Value, error) {
	if err := m.Budget.Step(scalars(v)); err != nil {
		return Value{}, err
	}
	return Str(ImageOf(v)), nil
}

// scalars counts the scalar subelements of v (1 for a scalar).
func scalars(v Value) int64 {
	if k := v.Kind(); k != KindArray && k != KindRecord {
		return 1
	}
	var n int64
	for _, e := range v.E {
		n += scalars(e)
	}
	return n
}

func arrayAttr(a *Attr, r Range) (Value, error) {
	rt := a.T
	if rt == nil {
		rt = UniversalInteger
	}
	switch a.Kind {
	case AttrLeft:
		return Value{T: rt, I: r.Left}, nil
	case AttrRight:
		return Value{T: rt, I: r.Right}, nil
	case AttrHigh:
		return Value{T: rt, I: r.High()}, nil
	case AttrLow:
		return Value{T: rt, I: r.Low()}, nil
	case AttrLength:
		n, err := r.CheckedLen()
		if err != nil {
			return Value{}, err
		}
		return Value{T: rt, I: n}, nil
	case AttrAscending:
		return Bool(r.Dir == To), nil
	}
	return Value{}, Unsupported("'" + a.Kind.String() + " of an array")
}

func scalarAttr(a *Attr, t *Type, arg Value) (Value, error) {
	rt := a.T
	if rt == nil {
		rt = t
	}
	if t.Kind == KindFloating {
		switch a.Kind {
		case AttrLeft:
			return RealValue(rt, t.Real.Left), nil
		case AttrRight:
			return RealValue(rt, t.Real.Right), nil
		case AttrHigh:
			return RealValue(rt, t.Real.High()), nil
		case AttrLow:
			return RealValue(rt, t.Real.Low()), nil
		case AttrAscending:
			return Bool(t.Real.Dir == To), nil
		case AttrImage:
			return Str(arg.Image()), nil
		}
		return Value{}, Unsupported("'" + a.Kind.String() +
			" of a floating type")
	}
	r := t.Range
	switch a.Kind {
	case AttrLeft:
		return Value{T: rt, I: r.Left}, nil
	case AttrRight:
		return Value{T: rt, I: r.Right}, nil
	case AttrHigh:
		return Value{T: rt, I: r.High()}, nil
	case AttrLow:
		return Value{T: rt, I: r.Low()}, nil
	case AttrAscending:
		return Bool(r.Dir == To), nil
	case AttrLength:
		n, err := r.CheckedLen()
		if err != nil {
			return Value{}, err
		}
		return Value{T: rt, I: n}, nil
	case AttrPos:
		return Check(Value{T: rt, I: arg.I}, rt)
	case AttrVal:
		return Check(Value{T: rt, I: arg.I}, t)
	case AttrSucc, AttrPred, AttrLeftof, AttrRightof:
		step := int64(1)
		if a.Kind == AttrPred ||
			(a.Kind == AttrLeftof && r.Dir == To) ||
			(a.Kind == AttrRightof && r.Dir == Downto) {
			step = -1
		}
		v, err := intArith(OpAdd, arg.I, step)
		if err != nil {
			// 'SUCC of the last value of a 64-bit type.
			return Value{}, RangeError(Value{T: t, I: arg.I},
				t.BaseType())
		}
		bt := t.BaseType()
		if a.Kind == AttrLeftof || a.Kind == AttrRightof {
			bt = t
		}
		if !bt.Range.Contains(v) {
			return Value{}, RangeError(Value{T: t, I: v}, bt)
		}
		return Value{T: rt, I: v}, nil
	case AttrImage:
		arg.T = t
		return Str(arg.Image()), nil
	case AttrValue:
		return parseValue(arg.GoString(), t)
	}
	return Value{}, Unsupported("'" + a.Kind.String())
}
