package eval

import (
	"math"
	"math/big"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
)

// edges are tried before random values (P5: generators try edge values
// first).
var edges = []int64{
	0, 1, -1, 2, -2, 3, 7, -7, 63, 64, math.MaxInt32, math.MinInt32,
	math.MaxInt32 + 1, math.MinInt32 - 1, math.MaxInt64, math.MinInt64,
	math.MaxInt64 - 1, math.MinInt64 + 1, 1 << 32, -(1 << 32),
	3037000499, 3037000500, -3037000500,
}

func operands(seed uint64, n int) []int64 {
	r := &env.SplitMix64{State: seed}
	out := append([]int64(nil), edges...)
	for range n {
		v := int64(r.Uint64())
		switch r.Uint64() % 3 {
		case 0:
			v >>= 33 // small
		case 1:
			v >>= 1
		}
		out = append(out, v)
	}
	return out
}

// TestIntArithBig checks exact integer arithmetic against math/big: the
// result is the mathematical value when it fits in 64 bits, else an
// overflow error (the reference model).
func TestIntArithBig(t *testing.T) {
	ops := []Op{OpAdd, OpSub, OpMul, OpDiv, OpRem, OpMod}
	vals := operands(1, 60)
	lim := new(big.Int).Lsh(big.NewInt(1), 63)
	minI, maxI := new(big.Int).Neg(lim), new(big.Int).Sub(lim,
		big.NewInt(1))
	for _, o := range ops {
		for _, x := range vals {
			for _, y := range vals {
				got, err := intArith(o, x, y)
				bx, by := big.NewInt(x), big.NewInt(y)
				var want *big.Int
				if y == 0 && (o == OpDiv || o == OpRem || o == OpMod) {
					if CodeOf(err) != ErrCodeDivZero {
						t.Fatalf("%d %v %d: err %v, want div by "+
							"zero", x, o, y, err)
					}
					continue
				}
				switch o {
				case OpAdd:
					want = new(big.Int).Add(bx, by)
				case OpSub:
					want = new(big.Int).Sub(bx, by)
				case OpMul:
					want = new(big.Int).Mul(bx, by)
				case OpDiv:
					want = new(big.Int).Quo(bx, by)
				case OpRem:
					want = new(big.Int).Rem(bx, by)
				case OpMod:
					want = new(big.Int).Rem(bx, by)
					if want.Sign() != 0 && want.Sign() != by.Sign() {
						want.Add(want, by)
					}
				}
				fits := want.Cmp(minI) >= 0 && want.Cmp(maxI) <= 0
				switch {
				case fits && err != nil:
					t.Fatalf("%d %v %d: unexpected %v", x, o, y, err)
				case fits && got != want.Int64():
					t.Fatalf("%d %v %d = %d, want %v", x, o, y, got,
						want)
				case !fits && CodeOf(err) != ErrCodeOverflow:
					t.Fatalf("%d %v %d: want overflow, got %d %v", x,
						o, y, got, err)
				}
			}
		}
	}
}

func TestPowBig(t *testing.T) {
	bases := []int64{0, 1, -1, 2, -2, 3, -3, 10, 255, -255, 1 << 31,
		math.MaxInt64, math.MinInt64}
	lim := new(big.Int).Lsh(big.NewInt(1), 63)
	for _, x := range bases {
		for y := int64(0); y <= 70; y++ {
			got, err := intArith(OpPow, x, y)
			want := new(big.Int).Exp(big.NewInt(x), big.NewInt(y), nil)
			fits := want.Cmp(new(big.Int).Neg(lim)) >= 0 &&
				want.Cmp(lim) < 0
			if fits != (err == nil) || (fits && got != want.Int64()) {
				t.Fatalf("%d ** %d = %d, %v; want %v", x, y, got, err,
					want)
			}
		}
	}
	if _, err := intArith(OpPow, 2, -1); CodeOf(err) != ErrCodeRange {
		t.Fatalf("2 ** -1: %v", err)
	}
}

// TestBaseRange: INTEGER is 32 bits, so a result outside it is an error
// even though the arithmetic is 64-bit.
func TestBaseRange(t *testing.T) {
	x := Int(Integer, math.MaxInt32)
	if _, err := Apply(nil, OpAdd, Integer, x, Int(Integer, 1)); CodeOf(
		err) != ErrCodeRange {
		t.Fatalf("MaxInt32+1: %v", err)
	}
	v, err := Apply(nil, OpAdd, UniversalInteger,
		Int(UniversalInteger, math.MaxInt32), Int(UniversalInteger, 1))
	if err != nil || v.I != math.MaxInt32+1 {
		t.Fatalf("universal: %v %v", v.I, err)
	}
	if _, err := Check(Int(Integer, -1), Natural); CodeOf(err) !=
		ErrCodeRange {
		t.Fatalf("natural -1: %v", err)
	}
}

func bitVec(bits string) Value {
	e := make([]Value, len(bits))
	for i := range len(bits) {
		e[i] = Enum(Bit, int64(bits[i]-'0'))
	}
	n := int64(len(bits))
	return Array(BitVector, []Range{{n - 1, 0, Downto}}, e)
}

func bitsOf(v Value) string {
	b := make([]byte, len(v.E))
	for i, e := range v.E {
		b[i] = byte('0' + e.I)
	}
	return string(b)
}

// TestCompareOrder: Compare is a total order consistent with Equal on
// integers, reals and bit vectors.
func TestCompareOrder(t *testing.T) {
	r := &env.SplitMix64{State: 7}
	var vals []Value
	for _, x := range operands(2, 20) {
		vals = append(vals, Int(UniversalInteger, x))
	}
	for range 20 {
		n := int(r.Uint64() % 5)
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('0' + r.Uint64()%2)
		}
		vals = append(vals, bitVec(string(b)))
	}
	for _, f := range []float64{0, -1.5, 1.5, math.MaxFloat64, 1e-300} {
		vals = append(vals, RealValue(Real, f))
	}
	for _, a := range vals {
		for _, b := range vals {
			if a.Kind() != b.Kind() {
				continue
			}
			ab, ba := a.Compare(b), b.Compare(a)
			if ab != -ba {
				t.Fatalf("antisymmetry: %s %s", a.Image(), b.Image())
			}
			if (ab == 0) != a.Equal(b) {
				t.Fatalf("Equal/Compare disagree: %s %s", a.Image(),
					b.Image())
			}
			for _, c := range vals {
				if c.Kind() != a.Kind() {
					continue
				}
				if ab <= 0 && b.Compare(c) <= 0 && a.Compare(c) > 0 {
					t.Fatalf("transitivity: %s %s %s", a.Image(),
						b.Image(), c.Image())
				}
			}
		}
	}
}

// TestShiftLaws: rotating by k then by -k is the identity, and sll by
// -k is srl by k.
func TestShiftLaws(t *testing.T) {
	for _, s := range []string{"", "1", "10", "1101", "100110101"} {
		v := bitVec(s)
		for k := int64(-12); k <= 12; k++ {
			a, _ := Apply(nil, OpRol, BitVector, v, Int(Integer, k))
			b, _ := Apply(nil, OpRol, BitVector, a, Int(Integer, -k))
			if bitsOf(b) != s {
				t.Fatalf("rol %d then %d of %q = %q", k, -k, s, bitsOf(b))
			}
			l, _ := Apply(nil, OpSll, BitVector, v, Int(Integer, -k))
			r, _ := Apply(nil, OpSrl, BitVector, v, Int(Integer, k))
			if bitsOf(l) != bitsOf(r) {
				t.Fatalf("sll -%d %q != srl %d", k, s, k)
			}
		}
	}
	v, _ := Apply(nil, OpSll, BitVector, bitVec("1011"), Int(Integer, 1))
	if bitsOf(v) != "0110" {
		t.Fatalf("1011 sll 1 = %s", bitsOf(v))
	}
	v, _ = Apply(nil, OpSra, BitVector, bitVec("1011"), Int(Integer, 2))
	if bitsOf(v) != "1110" {
		t.Fatalf("1011 sra 2 = %s", bitsOf(v))
	}
}

func TestConcat(t *testing.T) {
	// The index subtype's left bound and direction (LRM 9.2.5), as
	// GHDL and NVC give for any operand bounds: NATURAL, 0 to 3.
	v, err := Apply(nil, OpConcat, BitVector, bitVec("10"), bitVec("01"))
	if err != nil || bitsOf(v) != "1001" || v.B[0] != (Range{0, 3, To}) {
		t.Fatalf("10 & 01 = %s %v %v", bitsOf(v), v.B, err)
	}
	s, err := Apply(nil, OpConcat, String, Str("ab"), Str("cd"))
	if err != nil || s.GoString() != "abcd" || s.B[0] != (Range{1, 4,
		To}) {
		t.Fatalf("ab & cd = %q %v %v", s.GoString(), s.B, err)
	}
	e, err := Apply(nil, OpConcat, String, Enum(Character, 'x'),
		Str("yz"))
	if err != nil || e.GoString() != "xyz" || e.B[0] != (Range{1, 3,
		To}) {
		t.Fatalf("'x' & yz = %q %v %v", e.GoString(), e.B, err)
	}
	n, err := Apply(nil, OpConcat, String, Str(""), Str("q"))
	if err != nil || n.GoString() != "q" {
		t.Fatalf("\"\" & q = %q %v", n.GoString(), err)
	}
}

func TestLogicArrays(t *testing.T) {
	v, err := Apply(nil, OpXor, BitVector, bitVec("1100"), bitVec("1010"))
	if err != nil || bitsOf(v) != "0110" {
		t.Fatalf("xor: %s %v", bitsOf(v), err)
	}
	_, err = Apply(nil, OpAnd, BitVector, bitVec("11"), bitVec("101"))
	if CodeOf(err) != ErrCodeLength {
		t.Fatalf("and of different lengths: %v", err)
	}
	r, _ := Apply(nil, OpAnd, Bit, bitVec("111"))
	o, _ := Apply(nil, OpXor, Bit, bitVec("1011"))
	if r.I != 1 || o.I != 1 {
		t.Fatalf("reductions: and %d xor %d", r.I, o.I)
	}
}

// countingEnv loads every object as integer 5 and counts loads.
type countingEnv struct{ loads int }

func (c *countingEnv) Load(Ref) (Value, error) {
	c.loads++
	return Int(Integer, 5), nil
}

func (c *countingEnv) Call(Func, []Value, *Budget) (Value, error) {
	return Value{}, NotStatic("call")
}

func sample() Expr {
	five := &Load{Ref: "w", T: Integer}
	sum := &Operation{Op: OpAdd, T: Integer, Args: []Expr{five,
		&Const{V: Int(Integer, 3)}}}
	agg := &Aggregate{T: BitVector.ArraySubtype(Range{7, 0, Downto}),
		Assocs: []Assoc{{Choices: []Choice{{Kind: ChoiceOthers}},
			Value: &Const{V: Enum(Bit, 1)}}}}
	ln := &Attr{Kind: AttrLength, Of: agg, T: Integer}
	return &Operation{Op: OpMul, T: Integer, Args: []Expr{sum, ln}}
}

// TestBudgetExact: the step count is a function of the expression; a
// budget of exactly that many steps succeeds and one fewer fails with
// ErrBudget, every time.
func TestBudgetExact(t *testing.T) {
	m := &Machine{Env: &countingEnv{}, Budget: NewBudget(0)}
	v, err := m.Eval(sample())
	if err != nil || v.I != 64 {
		t.Fatalf("(5+3)*8 = %d %v", v.I, err)
	}
	n := m.Budget.Used()
	for range 3 {
		m := &Machine{Env: &countingEnv{}, Budget: NewBudget(n)}
		if _, err := m.Eval(sample()); err != nil ||
			m.Budget.Used() != n {
			t.Fatalf("budget %d: %v used %d", n, err, m.Budget.Used())
		}
		m = &Machine{Env: &countingEnv{}, Budget: NewBudget(n - 1)}
		if _, err := m.Eval(sample()); CodeOf(err) != ErrCodeBudget {
			t.Fatalf("budget %d: %v", n-1, err)
		}
	}
	if EditorSteps != 1_000_000 {
		t.Fatal("the editor limit is fixed at 1,000,000 steps (#34)")
	}
}

// TestBudgetLoop: a large aggregate charges its elements, so the
// editor budget stops it.
func TestBudgetLoop(t *testing.T) {
	big := &Aggregate{T: BitVector.ArraySubtype(Range{0, 5_000_000, To}),
		Assocs: []Assoc{{Choices: []Choice{{Kind: ChoiceOthers}},
			Value: &Const{V: Enum(Bit, 0)}}}}
	m := &Machine{Budget: NewBudget(EditorSteps)}
	if _, err := m.Eval(big); CodeOf(err) != ErrCodeBudget {
		t.Fatalf("5M-element aggregate under the editor budget: %v", err)
	}
}

func TestShortCircuit(t *testing.T) {
	e := &countingEnv{}
	m := &Machine{Env: e}
	x := &Operation{Op: OpAnd, T: Boolean, Args: []Expr{
		&Const{V: Bool(false)},
		&Operation{Op: OpEq, T: Boolean, Args: []Expr{
			&Load{Ref: "x", T: Integer}, &Const{V: Int(Integer, 1)}}}}}
	v, err := m.Eval(x)
	if err != nil || v.I != 0 || e.loads != 0 {
		t.Fatalf("false and ...: %v %v loads %d", v.I, err, e.loads)
	}
}

func TestAggregates(t *testing.T) {
	m := &Machine{}
	// (1 => '1', others => '0') of BIT_VECTOR(3 downto 0).
	agg := &Aggregate{T: BitVector.ArraySubtype(Range{3, 0, Downto}),
		Assocs: []Assoc{
			{Choices: []Choice{{Kind: ChoiceIndex,
				Index: &Const{V: Int(Integer, 1)}}},
				Value: &Const{V: Enum(Bit, 1)}},
			{Choices: []Choice{{Kind: ChoiceOthers}},
				Value: &Const{V: Enum(Bit, 0)}},
		}}
	v, err := m.Eval(agg)
	if err != nil || bitsOf(v) != "0010" {
		t.Fatalf("aggregate = %s %v", bitsOf(v), err)
	}
	// Positional into an unconstrained STRING starts at POSITIVE'LEFT.
	s := &Aggregate{T: String, Assocs: []Assoc{
		{Choices: []Choice{{Kind: ChoicePositional}},
			Value: &Const{V: Enum(Character, 'h')}},
		{Choices: []Choice{{Kind: ChoicePositional}},
			Value: &Const{V: Enum(Character, 'i')}},
	}}
	v, err = m.Eval(s)
	if err != nil || v.GoString() != "hi" || v.B[0] != (Range{1, 2, To}) {
		t.Fatalf("string aggregate = %q %v %v", v.GoString(), v.B, err)
	}
	// Index and slice.
	idx := &Index{X: agg, Idx: []Expr{&Const{V: Int(Integer, 1)}}}
	if v, err := m.Eval(idx); err != nil || v.I != 1 {
		t.Fatalf("agg(1) = %v %v", v.I, err)
	}
	bad := &Index{X: agg, Idx: []Expr{&Const{V: Int(Integer, 4)}}}
	if _, err := m.Eval(bad); CodeOf(err) != ErrCodeIndex {
		t.Fatalf("agg(4): %v", err)
	}
	sl := &Slice{X: agg, R: RangeExpr{Left: &Const{V: Int(Integer, 2)},
		Right: &Const{V: Int(Integer, 1)}, Dir: Downto}}
	if v, err := m.Eval(sl); err != nil || bitsOf(v) != "01" {
		t.Fatalf("agg(2 downto 1) = %s %v", bitsOf(v), err)
	}
	// A width mismatch on assignment-like checks.
	if _, err := Check(bitVec("101"),
		BitVector.ArraySubtype(Range{3, 0, Downto})); CodeOf(err) !=
		ErrCodeLength {
		t.Fatalf("3 bits into 4: %v", err)
	}
}

// TestImageValue: 'VALUE reads back every 'IMAGE of a scalar.
func TestImageValue(t *testing.T) {
	var vals []Value
	for _, x := range operands(3, 30) {
		if x >= math.MinInt32 && x <= math.MaxInt32 {
			vals = append(vals, Int(Integer, x))
		}
		vals = append(vals, Phys(Time, x))
	}
	for p := range int64(256) {
		vals = append(vals, Enum(Character, p))
	}
	vals = append(vals, Bool(true), Bool(false), Enum(SeverityLevel, 2),
		RealValue(Real, 1.5), RealValue(Real, -2e-7),
		RealValue(Real, 3))
	for _, v := range vals {
		img := v.Image()
		w, err := parseValue(img, v.T)
		if err != nil || !w.Equal(v) {
			t.Fatalf("%s'value(%q) = %s %v", v.T.Name, img, w.Image(),
				err)
		}
	}
}

func TestAttrs(t *testing.T) {
	m := &Machine{}
	sub := Integer.Subtype(Range{7, 0, Downto})
	cases := []struct {
		a    *Attr
		want int64
	}{
		{&Attr{Kind: AttrLeft, Type: sub}, 7},
		{&Attr{Kind: AttrLow, Type: sub}, 0},
		{&Attr{Kind: AttrLength, Type: sub}, 8},
		{&Attr{Kind: AttrSucc, Type: Integer,
			Arg: &Const{V: Int(Integer, 4)}}, 5},
		{&Attr{Kind: AttrLeftof, Type: sub,
			Arg: &Const{V: Int(Integer, 4)}}, 5},
		{&Attr{Kind: AttrPos, Type: Character, T: UniversalInteger,
			Arg: &Const{V: Enum(Character, 'A')}}, 65},
	}
	for i, c := range cases {
		v, err := m.Eval(c.a)
		if err != nil || v.I != c.want {
			t.Fatalf("case %d: %d %v, want %d", i, v.I, err, c.want)
		}
	}
	_, err := m.Eval(&Attr{Kind: AttrSucc, Type: Boolean,
		Arg: &Const{V: Bool(true)}})
	if CodeOf(err) != ErrCodeRange {
		t.Fatalf("true'succ: %v", err)
	}
	img, err := m.Eval(&Attr{Kind: AttrImage, Type: Time,
		Arg: &Const{V: Phys(Time, 5_000_000)}})
	if err != nil || img.GoString() != "5000000 fs" {
		t.Fatalf("time'image = %q %v", img.GoString(), err)
	}
}

func TestConvert(t *testing.T) {
	m := &Machine{}
	for _, c := range []struct {
		f    float64
		want int64
	}{{2.5, 3}, {-2.5, -3}, {2.49, 2}, {0.5, 1}} {
		v, err := m.Eval(&Convert{X: &Const{V: RealValue(Real, c.f)},
			T: Integer})
		if err != nil || v.I != c.want {
			t.Fatalf("integer(%v) = %d %v", c.f, v.I, err)
		}
	}
	if _, err := m.Eval(&Convert{X: &Const{V: RealValue(Real, 1e30)},
		T: Integer}); CodeOf(err) != ErrCodeRange {
		t.Fatalf("integer(1e30): %v", err)
	}
}

// TestAggregate2D: a two-dimensional aggregate of rows is one row-major
// value with both index ranges, and indexing reads it back.
func TestAggregate2D(t *testing.T) {
	m := &Machine{}
	tt := &Type{Kind: KindArray, Name: "T", Index: []*Type{Natural,
		Natural}, Elem: Integer}
	t2 := tt.ArraySubtype(Range{0, 1, To}, Range{0, 2, To})
	row := &Type{Kind: KindArray, Index: []*Type{Natural}, Elem: Integer}
	mk := func(a, b, c int64) Expr {
		var as []Assoc
		for _, x := range []int64{a, b, c} {
			as = append(as, Assoc{Choices: []Choice{{
				Kind: ChoicePositional}}, Value: &Const{V: Int(Integer, x)}})
		}
		return &Aggregate{T: row, Assocs: as}
	}
	agg := &Aggregate{T: t2, Assocs: []Assoc{
		{Choices: []Choice{{Kind: ChoicePositional}}, Value: mk(1, 2, 3)},
		{Choices: []Choice{{Kind: ChoicePositional}}, Value: mk(4, 5, 6)},
	}}
	v, err := m.Eval(agg)
	if err != nil || len(v.E) != 6 || len(v.B) != 2 {
		t.Fatalf("2-D aggregate: %v %v", v.Image(), err)
	}
	if _, err := Check(v, t2); err != nil {
		t.Fatalf("check: %v", err)
	}
	x := &Index{X: agg, Idx: []Expr{&Const{V: Int(Integer, 1)},
		&Const{V: Int(Integer, 2)}}}
	if e, err := m.Eval(x); err != nil || e.I != 6 {
		t.Fatalf("agg(1, 2) = %d %v", e.I, err)
	}
}
