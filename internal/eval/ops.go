package eval

import (
	"math"
	"math/bits"
	"strconv"
)

// Op is a predefined operation (LRM 9.2, 5.x implicit operations).
type Op uint8

// Predefined operations. The logical operators double as the unary
// reduction operators of VHDL-2008 when applied to one operand.
const (
	OpInvalid Op = iota
	OpAnd
	OpOr
	OpNand
	OpNor
	OpXor
	OpXnor
	OpNot
	OpEq
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe
	OpMatchEq
	OpMatchNe
	OpMatchLt
	OpMatchLe
	OpMatchGt
	OpMatchGe
	OpSll
	OpSrl
	OpSla
	OpSra
	OpRol
	OpRor
	OpAdd
	OpSub
	OpConcat
	OpPlus // unary +
	OpNeg  // unary -
	OpMul
	OpDiv
	OpMod
	OpRem
	OpPow
	OpAbs
	OpCondition // ?? (BIT to BOOLEAN)
	OpMinimum
	OpMaximum
)

var opNames = [...]string{
	OpInvalid: "invalid", OpAnd: "and", OpOr: "or", OpNand: "nand",
	OpNor: "nor", OpXor: "xor", OpXnor: "xnor", OpNot: "not",
	OpEq: "=", OpNe: "/=", OpLt: "<", OpLe: "<=", OpGt: ">", OpGe: ">=",
	OpMatchEq: "?=", OpMatchNe: "?/=", OpMatchLt: "?<", OpMatchLe: "?<=",
	OpMatchGt: "?>", OpMatchGe: "?>=", OpSll: "sll", OpSrl: "srl",
	OpSla: "sla", OpSra: "sra", OpRol: "rol", OpRor: "ror", OpAdd: "+",
	OpSub: "-", OpConcat: "&", OpPlus: "+", OpNeg: "-", OpMul: "*",
	OpDiv: "/", OpMod: "mod", OpRem: "rem", OpPow: "**", OpAbs: "abs",
	OpCondition: "??", OpMinimum: "minimum", OpMaximum: "maximum",
}

// String returns the operator symbol or name, for example "+" or
// "and".
func (o Op) String() string {
	if int(o) < len(opNames) {
		return opNames[o]
	}
	return "Op(" + strconv.Itoa(int(o)) + ")"
}

// Unary reports whether o takes one operand.
func (o Op) Unary() bool {
	return o == OpNot || o == OpPlus || o == OpNeg || o == OpAbs ||
		o == OpCondition
}

// Apply evaluates the predefined operation o on args with result type
// rt, charging b one step plus one per array element touched. Logical
// operators with one argument are the VHDL-2008 reductions.
func Apply(b *Budget, o Op, rt *Type, args ...Value) (Value, error) {
	if err := b.Step(1); err != nil {
		return Value{}, err
	}
	switch len(args) {
	case 1:
		return unary(b, o, rt, args[0])
	case 2:
		return binary(b, o, rt, args[0], args[1])
	}
	return Value{}, errf(ErrCodeType, o.String()+" with "+
		strconv.Itoa(len(args))+" operands")
}

func unary(b *Budget, o Op, rt *Type, x Value) (Value, error) {
	switch o {
	case OpPlus:
		return x, nil
	case OpNeg:
		switch x.Kind() {
		case KindFloating:
			return RealValue(rt, -x.F), nil
		case KindInteger, KindPhysical:
			if x.I == math.MinInt64 {
				return Value{}, overflow(o)
			}
			return checkBase(Value{T: rt, I: -x.I})
		}
	case OpAbs:
		switch x.Kind() {
		case KindFloating:
			return RealValue(rt, math.Abs(x.F)), nil
		case KindInteger, KindPhysical:
			if x.I == math.MinInt64 {
				return Value{}, overflow(o)
			}
			v := x.I
			if v < 0 {
				v = -v
			}
			return checkBase(Value{T: rt, I: v})
		}
	case OpNot:
		if x.Kind() == KindArray {
			if err := b.Step(int64(len(x.E))); err != nil {
				return Value{}, err
			}
			out := make([]Value, len(x.E))
			for i, e := range x.E {
				out[i] = Value{T: e.T, I: 1 - e.I}
			}
			return Value{T: rt, B: x.B, E: out}, nil
		}
		return Value{T: rt, I: 1 - x.I}, nil
	case OpCondition:
		return Bool(x.I != 0), nil
	case OpAnd, OpOr, OpNand, OpNor, OpXor, OpXnor:
		return reduce(b, o, rt, x)
	}
	return Value{}, typeErr(o, x)
}

// reduce is the VHDL-2008 unary logical reduction of a 1-D array.
func reduce(b *Budget, o Op, rt *Type, x Value) (Value, error) {
	if x.Kind() != KindArray {
		return Value{}, typeErr(o, x)
	}
	if err := b.Step(int64(len(x.E))); err != nil {
		return Value{}, err
	}
	base, neg := o, false
	switch o {
	case OpNand:
		base, neg = OpAnd, true
	case OpNor:
		base, neg = OpOr, true
	case OpXnor:
		base, neg = OpXor, true
	}
	acc := int64(0)
	if base == OpAnd {
		acc = 1
	}
	for _, e := range x.E {
		acc = logic(base, acc, e.I)
	}
	if neg {
		acc = 1 - acc
	}
	return Value{T: rt, I: acc}, nil
}

func logic(o Op, l, r int64) int64 {
	var v bool
	switch o {
	case OpAnd, OpNand:
		v = l != 0 && r != 0
	case OpOr, OpNor:
		v = l != 0 || r != 0
	case OpXor, OpXnor:
		v = (l != 0) != (r != 0)
	}
	if o == OpNand || o == OpNor || o == OpXnor {
		v = !v
	}
	if v {
		return 1
	}
	return 0
}

func binary(b *Budget, o Op, rt *Type, x, y Value) (Value, error) {
	switch o {
	case OpAnd, OpOr, OpNand, OpNor, OpXor, OpXnor:
		if x.Kind() == KindArray {
			return elementwise(b, o, rt, x, y)
		}
		if y.Kind() == KindArray { // 2008 scalar-array forms
			return elementwise(b, o, rt, y, x)
		}
		return Value{T: rt, I: logic(o, x.I, y.I)}, nil
	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		if err := b.Step(int64(min(len(x.E), len(y.E)))); err != nil {
			return Value{}, err
		}
		return Bool(relation(o, x, y)), nil
	case OpMatchEq, OpMatchNe, OpMatchLt, OpMatchLe, OpMatchGt,
		OpMatchGe:
		return matching(b, o, rt, x, y)
	case OpMinimum, OpMaximum:
		if err := b.Step(int64(min(len(x.E), len(y.E)))); err != nil {
			return Value{}, err
		}
		c := x.Compare(y)
		if (o == OpMinimum) == (c <= 0) {
			return x, nil
		}
		return y, nil
	case OpConcat:
		return concat(b, rt, x, y)
	case OpSll, OpSrl, OpSla, OpSra, OpRol, OpRor:
		return shift(b, o, rt, x, y)
	case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpRem, OpPow:
		return arith(o, rt, x, y)
	}
	return Value{}, typeErr(o, x)
}

func relation(o Op, x, y Value) bool {
	if o == OpEq {
		return x.Equal(y)
	}
	if o == OpNe {
		return !x.Equal(y)
	}
	c := x.Compare(y)
	switch o {
	case OpLt:
		return c < 0
	case OpLe:
		return c <= 0
	case OpGt:
		return c > 0
	}
	return c >= 0
}

// matching implements the VHDL-2008 matching relational operators of
// BIT (and the "?=" of BIT arrays): the result is a BIT.
func matching(b *Budget, o Op, rt *Type, x, y Value) (Value, error) {
	if isULogic(rt) {
		return matchingULogic(b, o, rt, x, y)
	}
	var r bool
	switch o {
	case OpMatchEq, OpMatchNe:
		if x.Kind() == KindArray && len(x.E) != len(y.E) {
			return Value{}, LengthError(int64(len(x.E)),
				int64(len(y.E)))
		}
		if err := b.Step(int64(len(x.E))); err != nil {
			return Value{}, err
		}
		r = x.Equal(y)
		if o == OpMatchNe {
			r = !r
		}
	default:
		r = relation(map[Op]Op{OpMatchLt: OpLt, OpMatchLe: OpLe,
			OpMatchGt: OpGt, OpMatchGe: OpGe}[o], x, y)
	}
	if r {
		return Value{T: rt, I: 1}, nil
	}
	return Value{T: rt}, nil
}

func elementwise(b *Budget, o Op, rt *Type, x, y Value) (Value, error) {
	if err := b.Step(int64(len(x.E))); err != nil {
		return Value{}, err
	}
	out := make([]Value, len(x.E))
	if y.Kind() == KindArray {
		if len(x.E) != len(y.E) {
			return Value{}, LengthError(int64(len(x.E)),
				int64(len(y.E)))
		}
		for i := range x.E {
			out[i] = Value{T: x.E[i].T, I: logic(o, x.E[i].I, y.E[i].I)}
		}
	} else {
		for i := range x.E {
			out[i] = Value{T: x.E[i].T, I: logic(o, x.E[i].I, y.I)}
		}
	}
	return Value{T: rt, B: x.B, E: out}, nil
}

// concat implements "&" (LRM 9.2.5) for one-dimensional arrays: each
// operand is an array of rt or an element of it. A null left operand
// gives the right operand.
func concat(b *Budget, rt *Type, x, y Value) (Value, error) {
	base := rt.BaseType()
	isArr := func(v Value) bool {
		return v.Kind() == KindArray && v.T.SameBase(base)
	}
	elems := func(v Value) []Value {
		if isArr(v) {
			return v.E
		}
		return []Value{v}
	}
	ex, ey := elems(x), elems(y)
	if err := b.Step(int64(len(ex) + len(ey))); err != nil {
		return Value{}, err
	}
	if isArr(x) && len(ex) == 0 && isArr(y) {
		return y, nil
	}
	out := make([]Value, 0, len(ex)+len(ey))
	out = append(append(out, ex...), ey...)
	// The result's left bound and direction are those of the index
	// subtype (LRM 9.2.5), what GHDL and NVC give under every standard.
	var idx Range
	switch {
	case len(base.Index) == 1:
		is := base.Index[0]
		idx = Range{Left: is.Range.Left, Dir: is.Range.Dir}
	default:
		idx = Range{Left: 0}
	}
	n := int64(len(out))
	if n == 0 {
		idx.Right = idx.At(-1)
	} else {
		idx.Right = idx.At(n - 1)
	}
	if len(base.Index) == 1 && n > 0 &&
		!base.Index[0].Range.Contains(idx.Right) {
		return Value{}, errf(ErrCodeIndex, "concatenation result "+
			idx.String()+" leaves the index range "+
			base.Index[0].Range.String())
	}
	return Value{T: base, B: []Range{idx}, E: out}, nil
}

// shift implements the VHDL shift and rotate operators on 1-D arrays of
// BIT or BOOLEAN (LRM 9.2.4).
func shift(b *Budget, o Op, rt *Type, x, y Value) (Value, error) {
	if x.Kind() != KindArray {
		return Value{}, typeErr(o, x)
	}
	n := int64(len(x.E))
	if err := b.Step(n); err != nil {
		return Value{}, err
	}
	out := make([]Value, n)
	if n == 0 {
		return Value{T: rt, B: x.B, E: out}, nil
	}
	k := y.I
	if k < 0 {
		k = -k
		switch o {
		case OpSll:
			o = OpSrl
		case OpSrl:
			o = OpSll
		case OpSla:
			o = OpSra
		case OpSra:
			o = OpSla
		case OpRol:
			o = OpRor
		case OpRor:
			o = OpRol
		}
	}
	var zero Value
	if et := x.T.BaseType().Elem; et != nil {
		zero = Value{T: et, I: et.BaseType().Range.Left}
	}
	for i := range n {
		var src int64
		fill := zero
		switch o {
		case OpSll, OpSla:
			src = i + k
			if o == OpSla {
				fill = x.E[n-1]
			}
		case OpSrl, OpSra:
			src = i - k
			if o == OpSra {
				fill = x.E[0]
			}
		case OpRol:
			src = (i + k%n) % n
		case OpRor:
			src = ((i-k%n)%n + n) % n
		}
		if src >= 0 && src < n {
			out[i] = x.E[src]
		} else {
			out[i] = fill
		}
	}
	return Value{T: rt, B: x.B, E: out}, nil
}

func arith(o Op, rt *Type, x, y Value) (Value, error) {
	xk, yk := x.Kind(), y.Kind()
	switch {
	case xk == KindFloating && yk == KindFloating:
		return realArith(o, rt, x.F, y.F)
	case xk == KindFloating && yk == KindInteger && o == OpPow:
		return RealValue(rt, math.Pow(x.F, float64(y.I))), nil
	case xk == KindPhysical && yk == KindFloating:
		return physReal(o, rt, x.I, y.F)
	case xk == KindFloating && yk == KindPhysical && o == OpMul:
		return physReal(o, rt, y.I, x.F)
	case xk == KindPhysical && yk == KindPhysical && o == OpDiv:
		if y.I == 0 {
			return Value{}, divZero()
		}
		return Int(rt, x.I/y.I), nil
	case (xk == KindInteger || xk == KindPhysical) &&
		(yk == KindInteger || yk == KindPhysical):
		v, err := intArith(o, x.I, y.I)
		if err != nil {
			return Value{}, err
		}
		return checkBase(Value{T: rt, I: v})
	}
	return Value{}, typeErr(o, x)
}

func realArith(o Op, rt *Type, x, y float64) (Value, error) {
	var v float64
	switch o {
	case OpAdd:
		v = x + y
	case OpSub:
		v = x - y
	case OpMul:
		v = x * y
	case OpDiv:
		if y == 0 {
			return Value{}, divZero()
		}
		v = x / y
	default:
		return Value{}, errf(ErrCodeType, o.String()+
			" is not defined for floating types")
	}
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return Value{}, overflow(o)
	}
	return RealValue(rt, v), nil
}

func physReal(o Op, rt *Type, p int64, f float64) (Value, error) {
	var v float64
	switch o {
	case OpMul:
		v = float64(p) * f
	case OpDiv:
		if f == 0 {
			return Value{}, divZero()
		}
		v = float64(p) / f
	default:
		return Value{}, errf(ErrCodeType, o.String()+
			" is not defined for physical and real operands")
	}
	v = math.Round(v)
	if v >= math.MaxInt64 || v < math.MinInt64 || math.IsNaN(v) {
		return Value{}, overflow(o)
	}
	return checkBase(Value{T: rt, I: int64(v)})
}

// intArith is exact 64-bit integer arithmetic with overflow detection.
// rem takes the sign of the left operand and mod the sign of the right
// (LRM 9.2.7).
func intArith(o Op, x, y int64) (int64, error) {
	switch o {
	case OpAdd:
		s := x + y
		if (x >= 0) == (y >= 0) && (s >= 0) != (x >= 0) {
			return 0, overflow(o)
		}
		return s, nil
	case OpSub:
		d := x - y
		if (x >= 0) != (y >= 0) && (d >= 0) != (x >= 0) {
			return 0, overflow(o)
		}
		return d, nil
	case OpMul:
		return mul64(x, y)
	case OpDiv, OpRem, OpMod:
		if y == 0 {
			return 0, divZero()
		}
		if x == math.MinInt64 && y == -1 {
			if o == OpDiv {
				return 0, overflow(o)
			}
			return 0, nil
		}
		switch o {
		case OpDiv:
			return x / y, nil
		case OpRem:
			return x % y, nil
		}
		m := x % y
		if m != 0 && (m < 0) != (y < 0) {
			m += y
		}
		return m, nil
	case OpPow:
		if y < 0 {
			return 0, errf(ErrCodeRange,
				"negative exponent "+strconv.FormatInt(y, 10)+
					" for an integer base")
		}
		// Square-and-multiply. Once base*base overflows, a later bit
		// of the exponent uses it, so the result overflows too.
		r, base := int64(1), x
		for e := y; e > 0; {
			var err error
			if e&1 == 1 {
				if r, err = mul64(r, base); err != nil {
					return 0, overflow(o)
				}
			}
			if e >>= 1; e > 0 {
				if base, err = mul64(base, base); err != nil {
					return 0, overflow(o)
				}
			}
		}
		return r, nil
	}
	return 0, errf(ErrCodeType, o.String()+" is not an arithmetic "+
		"operator")
}

func mul64(x, y int64) (int64, error) {
	if x == 0 || y == 0 {
		return 0, nil
	}
	neg := (x < 0) != (y < 0)
	ux, uy := abs64(x), abs64(y)
	hi, lo := bits.Mul64(ux, uy)
	if hi != 0 {
		return 0, overflow(OpMul)
	}
	if neg {
		if lo > 1<<63 {
			return 0, overflow(OpMul)
		}
		return int64(-lo), nil // lo == 1<<63 gives MinInt64
	}
	if lo >= 1<<63 {
		return 0, overflow(OpMul)
	}
	return int64(lo), nil
}

func abs64(x int64) uint64 {
	if x < 0 {
		return uint64(-x) // MinInt64 wraps to 1<<63, as wanted
	}
	return uint64(x)
}

// checkBase checks an integer or physical result against the range of
// its base type: overflowing INTEGER (32 bits) is an error even
// before any subtype check (LRM 5.2.3.1).
func checkBase(v Value) (Value, error) {
	if v.T == nil {
		return v, nil
	}
	bt := v.T.BaseType()
	if (bt.Kind == KindInteger || bt.Kind == KindPhysical) &&
		!bt.Range.Contains(v.I) {
		return Value{}, RangeError(v, bt)
	}
	return v, nil
}

func overflow(o Op) error {
	return errf(ErrCodeOverflow, "overflow in "+o.String())
}

func divZero() error { return errf(ErrCodeDivZero, "division by zero") }

func typeErr(o Op, x Value) error {
	return errf(ErrCodeType, o.String()+" is not defined for "+
		x.Kind().String()+" operands")
}

// isULogic reports whether t is STD_ULOGIC: the nine-valued
// enumeration 'U', 'X', '0', '1', 'Z', 'W', 'L', 'H', '-'.
func isULogic(t *Type) bool {
	return t != nil && t.Kind == KindEnum && len(t.Literals) == 9 &&
		t.Literals[0] == "'U'" && t.Literals[8] == "'-'"
}

// STD_ULOGIC positions.
const (
	uU int64 = iota
	uX
	u0
	u1
)

// uMatchEq is the "?=" of STD_ULOGIC (LRM 9.2.3), by position.
var uMatchEq = [9][9]int64{
	{uU, uU, uU, uU, uU, uU, uU, uU, u1},
	{uU, uX, uX, uX, uX, uX, uX, uX, u1},
	{uU, uX, u1, u0, uX, uX, u1, u0, u1},
	{uU, uX, u0, u1, uX, uX, u0, u1, u1},
	{uU, uX, uX, uX, uX, uX, uX, uX, u1},
	{uU, uX, uX, uX, uX, uX, uX, uX, u1},
	{uU, uX, u1, u0, uX, uX, u1, u0, u1},
	{uU, uX, u0, u1, uX, uX, u0, u1, u1},
	{u1, u1, u1, u1, u1, u1, u1, u1, u1},
}

// uMatchLt is the "?<" of STD_ULOGIC (LRM 9.2.3); a '-' operand is an
// error.
var uMatchLt = [9][9]int64{
	{uU, uU, uU, uU, uU, uU, uU, uU, uX},
	{uU, uX, uX, uX, uX, uX, uX, uX, uX},
	{uU, uX, u0, u1, uX, uX, u0, u1, uX},
	{uU, uX, u0, u0, uX, uX, u0, u0, uX},
	{uU, uX, uX, uX, uX, uX, uX, uX, uX},
	{uU, uX, uX, uX, uX, uX, uX, uX, uX},
	{uU, uX, u0, u1, uX, uX, u0, u1, uX},
	{uU, uX, u0, u0, uX, uX, u0, u0, uX},
	{uX, uX, uX, uX, uX, uX, uX, uX, uX},
}

// uAnd is the "and" of the results of "?=" ('U', 'X', '0', '1').
func uAnd(l, r int64) int64 {
	switch {
	case l == u0 || r == u0:
		return u0
	case l == uU || r == uU:
		return uU
	case l == uX || r == uX:
		return uX
	}
	return u1
}

func uNot(v int64) int64 {
	switch v {
	case u0:
		return u1
	case u1:
		return u0
	}
	return v
}

// matchingULogic implements the matching operators of STD_ULOGIC and
// "?=", "?/=" of STD_ULOGIC arrays (the "and" of the element results).
func matchingULogic(b *Budget, o Op, rt *Type, x, y Value) (Value,
	error,
) {
	pos := func(v Value) (int64, error) {
		if v.I < 0 || v.I > 8 {
			return 0, typeErr(o, v)
		}
		return v.I, nil
	}
	eq := func(l, r Value) (int64, error) {
		a, err := pos(l)
		if err != nil {
			return 0, err
		}
		c, err := pos(r)
		if err != nil {
			return 0, err
		}
		return uMatchEq[a][c], nil
	}
	var r int64
	switch o {
	case OpMatchEq, OpMatchNe:
		if x.Kind() == KindArray {
			if len(x.E) != len(y.E) {
				return Value{}, LengthError(int64(len(x.E)),
					int64(len(y.E)))
			}
			if err := b.Step(int64(len(x.E))); err != nil {
				return Value{}, err
			}
			r = u1
			for i := range x.E {
				e, err := eq(x.E[i], y.E[i])
				if err != nil {
					return Value{}, err
				}
				r = uAnd(r, e)
			}
		} else {
			e, err := eq(x, y)
			if err != nil {
				return Value{}, err
			}
			r = e
		}
		if o == OpMatchNe {
			r = uNot(r)
		}
	default:
		a, err := pos(x)
		if err != nil {
			return Value{}, err
		}
		c, err := pos(y)
		if err != nil {
			return Value{}, err
		}
		if a == 8 || c == 8 {
			return Value{}, errf(ErrCodeRange, "'-' operand of the "+
				"matching ordering operator "+o.String())
		}
		lt := uMatchLt[a][c]
		le := uOr(lt, uMatchEq[a][c])
		switch o {
		case OpMatchLt:
			r = lt
		case OpMatchLe:
			r = le
		case OpMatchGt:
			r = uNot(le)
		default:
			r = uNot(lt)
		}
	}
	return Value{T: rt, I: r}, nil
}

func uOr(l, r int64) int64 {
	switch {
	case l == u1 || r == u1:
		return u1
	case l == uU || r == uU:
		return uU
	case l == uX || r == uX:
		return uX
	}
	return u0
}
