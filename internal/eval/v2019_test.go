package eval

import (
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/token"
)

// TestStdIntegers: VHDL-2019 gets the 64-bit triple, every older
// standard the 32-bit one, and both are named INTEGER (traces and
// messages do not change). STRING's index stays the 32-bit POSITIVE.
func TestStdIntegers(t *testing.T) {
	for _, std := range token.Standards {
		i, n, p := StdIntegers(std)
		lo, hi := int64(math.MinInt32), int64(math.MaxInt32)
		if std >= token.Std2019 {
			lo, hi = math.MinInt64, math.MaxInt64
		}
		if i.Range != (Range{lo, hi, To}) ||
			n.Range != (Range{0, hi, To}) ||
			p.Range != (Range{1, hi, To}) {
			t.Fatalf("%d: %v %v %v", std, i.Range, n.Range, p.Range)
		}
		for _, x := range []*Type{i, n, p} {
			if x.Name != "INTEGER" || x.BaseType() != i ||
				x.Kind != KindInteger {
				t.Fatalf("%d: %+v", std, x)
			}
		}
		if std < token.Std2019 &&
			(i != Integer || n != Natural || p != Positive) {
			t.Fatalf("%d: not the shared 32-bit layouts", std)
		}
	}
	if String.Index[0] != Positive || BitVector.Index[0] != Natural {
		t.Fatal("STRING/BIT_VECTOR index subtypes changed")
	}
}

// TestInt64Edge: at the edge of the 64-bit INTEGER every operation
// gives the exact value or a clean error, never a wrapped value.
func TestInt64Edge(t *testing.T) {
	b := &Budget{}
	hi, lo := Int(Integer64, math.MaxInt64), Int(Integer64, math.MinInt64)
	one, two := Int(Integer64, 1), Int(UniversalInteger, 2)
	errs := []struct {
		name string
		err  func() error
		code Code
	}{
		{"integer'high+1", func() error {
			_, err := Apply(b, OpAdd, Integer64, hi, one)
			return err
		}, ErrCodeOverflow},
		{"integer'low-1", func() error {
			_, err := Apply(b, OpSub, Integer64, lo, one)
			return err
		}, ErrCodeOverflow},
		{"-integer'low", func() error {
			_, err := Apply(b, OpNeg, Integer64, lo)
			return err
		}, ErrCodeOverflow},
		{"abs integer'low", func() error {
			_, err := Apply(b, OpAbs, Integer64, lo)
			return err
		}, ErrCodeOverflow},
		{"2**63 (as in -2**63)", func() error {
			_, err := Apply(b, OpPow, UniversalInteger, two,
				Int(UniversalInteger, 63))
			return err
		}, ErrCodeOverflow},
		{"integer'low/-1", func() error {
			_, err := Apply(b, OpDiv, Integer64, lo, Int(Integer64, -1))
			return err
		}, ErrCodeOverflow},
		{"integer'range'length", func() error {
			_, err := (&Machine{}).Eval(&Attr{Kind: AttrLength,
				Type: Integer64, T: UniversalInteger})
			return err
		}, ErrCodeOverflow},
		{"natural'range'length", func() error {
			_, err := (&Machine{}).Eval(&Attr{Kind: AttrLength,
				Type: Natural64, T: UniversalInteger})
			return err
		}, ErrCodeOverflow},
		{"integer'succ(integer'high)", func() error {
			_, err := (&Machine{}).Eval(&Attr{Kind: AttrSucc,
				Type: Integer64, Arg: &Const{V: hi}})
			return err
		}, ErrCodeRange},
		{"integer'pred(integer'low)", func() error {
			_, err := (&Machine{}).Eval(&Attr{Kind: AttrPred,
				Type: Integer64, Arg: &Const{V: lo}})
			return err
		}, ErrCodeRange},
		{"32-bit integer'high+1", func() error {
			_, err := Apply(b, OpAdd, Integer,
				Int(Integer, math.MaxInt32), Int(Integer, 1))
			return err
		}, ErrCodeRange},
		{"2**40 in 32-bit integer", func() error {
			_, err := Apply(b, OpPow, Integer, Int(Integer, 2),
				Int(Integer, 40))
			return err
		}, ErrCodeRange},
	}
	for _, c := range errs {
		if err := c.err(); CodeOf(err) != c.code {
			t.Errorf("%s: %v, want code %d", c.name, err, c.code)
		}
	}
	oks := []struct {
		name string
		e    Expr
		want int64
	}{
		{"2**62*2-1+2**62*2", &Operation{Op: OpAdd, T: Integer64,
			Args: []Expr{
				&Operation{Op: OpSub, T: Integer64, Args: []Expr{
					&Operation{Op: OpMul, T: Integer64, Args: []Expr{
						&Const{V: Int(Integer64, 1<<62)},
						&Const{V: Int(Integer64, 1)}}},
					&Const{V: Int(Integer64, 1)}}},
				&Const{V: Int(Integer64, 1<<62)}}}, math.MaxInt64},
		{"-2**62*2", &Operation{Op: OpMul, T: Integer64, Args: []Expr{
			&Const{V: Int(Integer64, -(1 << 62))},
			&Const{V: Int(Integer64, 2)}}}, math.MinInt64},
		{"2**40", &Operation{Op: OpPow, T: Integer64, Args: []Expr{
			&Const{V: Int(Integer64, 2)},
			&Const{V: Int(Integer64, 40)}}}, 1 << 40},
		{"positive'range'length", &Attr{Kind: AttrLength,
			Type: Positive64, T: UniversalInteger}, math.MaxInt64},
		{"integer'high", &Attr{Kind: AttrHigh, Type: Integer64},
			math.MaxInt64},
		{"integer'pred(integer'high)", &Attr{Kind: AttrPred,
			Type: Integer64, Arg: &Const{V: hi}}, math.MaxInt64 - 1},
	}
	for _, c := range oks {
		v, err := (&Machine{}).Eval(c.e)
		if err != nil || v.I != c.want {
			t.Errorf("%s = %d %v, want %d", c.name, v.I, err, c.want)
		}
	}
	img, err := (&Machine{}).Eval(&Attr{Kind: AttrImage,
		Type: Integer64, Arg: &Const{V: lo}})
	if err != nil || img.GoString() != "-9223372036854775808" {
		t.Errorf("integer'image(integer'low) = %q %v",
			img.GoString(), err)
	}
	v, err := parseValue("9223372036854775807", Integer64)
	if err != nil || v.I != math.MaxInt64 {
		t.Errorf("integer'value(high) = %v %v", v.I, err)
	}
	if _, err := parseValue("9223372036854775808",
		Integer64); CodeOf(err) != ErrCodeRange {
		t.Errorf("integer'value(high+1): %v", err)
	}
}

// TestCheckedLen: CheckedLen is the exact count (math/big) when it
// fits in an int64, an overflow error otherwise, and agrees with Len
// whenever it succeeds.
func TestCheckedLen(t *testing.T) {
	vals := operands(7, 40)
	maxI := big.NewInt(math.MaxInt64)
	for _, l := range vals {
		for _, r := range vals {
			for _, d := range []Dir{To, Downto} {
				rg := Range{l, r, d}
				n, err := rg.CheckedLen()
				want := big.NewInt(0)
				if !rg.Null() {
					want.Sub(big.NewInt(rg.High()),
						big.NewInt(rg.Low()))
					want.Add(want, big.NewInt(1))
				}
				fits := want.Cmp(maxI) <= 0
				switch {
				case fits && (err != nil || n != want.Int64()):
					t.Fatalf("%v: %d %v, want %v", rg, n, err, want)
				case !fits && CodeOf(err) != ErrCodeOverflow:
					t.Fatalf("%v: %d %v, want overflow", rg, n, err)
				case fits && n != rg.Len():
					t.Fatalf("%v: Len %d != %d", rg, rg.Len(), n)
				}
			}
		}
	}
}

// Test types for composite values.
var (
	recT = &Type{Kind: KindRecord, Name: "REC",
		Fields: []Field{{"a", Integer}, {"b", Bit}}}
	intVecT = &Type{Kind: KindArray, Name: "IARR",
		Index: []*Type{Natural}, Elem: Integer}
	ulogicT = &Type{Kind: KindEnum, Name: "STD_ULOGIC",
		Literals: []string{"'U'", "'X'", "'0'", "'1'", "'Z'", "'W'",
			"'L'", "'H'", "'-'"}, Range: Range{0, 8, To}}
	sulvT = &Type{Kind: KindArray, Name: "STD_ULOGIC_VECTOR",
		Index: []*Type{Natural}, Elem: ulogicT}
	quoteT = &Type{Kind: KindEnum, Name: "QE",
		Literals: []string{"'\"'", "'a'", "'b'"}, Range: Range{0, 2, To}}
	quoteVecT = &Type{Kind: KindArray, Name: "QV",
		Index: []*Type{Natural}, Elem: quoteT}
	mixedT = &Type{Kind: KindEnum, Name: "ME",
		Literals: []string{"'x'", "idle"}, Range: Range{0, 1, To}}
	mixedVecT = &Type{Kind: KindArray, Name: "MV",
		Index: []*Type{Natural}, Elem: mixedT}
	// An array of unconstrained BIT_VECTORs (2008 open element).
	bvVecT = &Type{Kind: KindArray, Name: "BVV",
		Index: []*Type{Natural}, Elem: BitVector}
	// A record with an unconstrained field.
	openRecT = &Type{Kind: KindRecord, Name: "OPEN_REC",
		Fields: []Field{{"n", Integer}, {"v", BitVector},
			{"s", String}}}
	m2T = &Type{Kind: KindArray, Name: "M2",
		Index: []*Type{Integer, Integer}, Elem: Integer}
	bm2T = &Type{Kind: KindArray, Name: "BM",
		Index: []*Type{Integer, Integer}, Elem: Bit}
)

func rec(a int64, b int64) Value {
	return Record(recT, []Value{Int(Integer, a), Enum(Bit, b)})
}

func ints(t *Type, l int64, xs ...int64) Value {
	e := make([]Value, len(xs))
	for i, x := range xs {
		e[i] = Int(Integer, x)
	}
	return Array(t, []Range{{l, l + int64(len(xs)) - 1, To}}, e)
}

func enums(t, et *Type, lits ...int64) Value {
	e := make([]Value, len(lits))
	for i, x := range lits {
		e[i] = Enum(et, x)
	}
	return Array(t, []Range{{0, int64(len(lits)) - 1, To}}, e)
}

func list(t *Type, l int64, xs ...Value) Value {
	return Array(t, []Range{{l, l + int64(len(xs)) - 1, To}}, xs)
}

// TestImageOf: composite 'IMAGE matches NVC 1.23 --std=2019 byte for
// byte (each want was printed by nvc on the same design), except
// where NVC loses data (noted).
func TestImageOf(t *testing.T) {
	strArr := &Type{Kind: KindArray, Name: "SARR",
		Index: []*Type{Natural}, Elem: String.ArraySubtype(
			Range{1, 2, To})}
	recArr := &Type{Kind: KindArray, Name: "RARR",
		Index: []*Type{Natural}, Elem: recT}
	nestT := &Type{Kind: KindRecord, Name: "NEST", Fields: []Field{
		{"r", recT}, {"s", String}, {"v", BitVector}, {"t", Time},
		{"e", Boolean}, {"c", Character}}}
	crT := &Type{Kind: KindRecord, Name: "CR", Fields: []Field{
		{"c", Character}, {"s", String}}}
	crArr := &Type{Kind: KindArray, Name: "CRA",
		Index: []*Type{Natural}, Elem: crT}
	rvT := &Type{Kind: KindRecord, Name: "RV",
		Fields: []Field{{"v", BitVector}}}
	timeArr := &Type{Kind: KindArray, Name: "PA",
		Index: []*Type{Natural}, Elem: Time}
	boolArr := &Type{Kind: KindArray, Name: "EA",
		Index: []*Type{Natural}, Elem: Boolean}
	vecVec := &Type{Kind: KindArray, Name: "IAA",
		Index: []*Type{Natural}, Elem: intVecT}
	m2 := Array(m2T, []Range{{1, 2, To}, {1, 2, To}}, []Value{
		Int(Integer, 1), Int(Integer, 2), Int(Integer, 3),
		Int(Integer, 4)})
	bm := Array(bm2T, []Range{{1, 2, To}, {1, 2, To}}, []Value{
		Enum(Bit, 0), Enum(Bit, 1), Enum(Bit, 1), Enum(Bit, 0)})
	cases := []struct {
		v    Value
		want string
	}{
		{rec(7, 1), "(7,'1')"},
		{Record(nestT, []Value{rec(-3, 0), Str("a\"b"), bitVec("1010"),
			Phys(Time, 5_000_000), Bool(true), Enum(Character, 'q')}),
			`((-3,'0'),(a,",b),"1010",5000000 fs,true,'q')`},
		{bitVec("1010"), `"1010"`},
		{Str("ab\"c"), `(a,b,",c)`},
		{ints(intVecT, 0, 1, -2, 3), "(1,-2,3)"},
		{list(recArr, 1, rec(1, 0), rec(2, 1)), "((1,'0'),(2,'1'))"},
		{Str("xy"), "(x,y)"},
		{list(strArr, 1, Str("ab"), Str("cd")), "((a,b),(c,d))"},
		{list(boolArr, 1, Bool(true), Bool(false)), "(true,false)"},
		{enums(quoteVecT, quoteT, 1, 0, 2), `"a"b"`},
		{enums(mixedVecT, mixedT, 0, 1), "(x,idle)"},
		{enums(sulvT, ulogicT, 2, 3, 4, 1), `"01ZX"`},
		{list(crArr, 1,
			Record(crT, []Value{Enum(Character, 'a'), Str("x\"")}),
			Record(crT, []Value{Enum(Character, 0), Str("yz")})),
			`(('a',(x,")),(nul,(y,z)))`},
		{Record(rvT, []Value{bitVec("01")}), `("01")`},
		{list(timeArr, 1, Phys(Time, 1_000_000), Phys(Time, 2000)),
			"(1000000 fs,2000 fs)"},
		{Array(String, []Range{{1, 2, To}}, []Value{
			Enum(Character, 0), Enum(Character, 'a')}), "(nul,a)"},
		{list(vecVec, 1, ints(intVecT, 1, 1, 2), ints(intVecT, 1, 3, 4)),
			"((1,2),(3,4))"},
		// NVC 1.23 prints only the first row of a 2-D array ((1,2)
		// and "01") and a lone ")" for a null array; GoHard writes the
		// whole value as a nested aggregate.
		{m2, "((1,2),(3,4))"},
		{bm, `("01","10")`},
		{Array(intVecT, []Range{{1, 0, To}}, nil), "()"},
		{Array(BitVector, []Range{{1, 0, To}}, nil), `""`},
		{Array(m2T, []Range{{1, 0, To}, {1, 2, To}}, nil), "()"},
	}
	for _, c := range cases {
		if got := ImageOf(c.v); got != c.want {
			t.Errorf("%s'image = %s, want %s", c.v.T.Name, got, c.want)
		}
	}
	// Image (report messages, goldens) is unchanged.
	if got := rec(7, 1).Image(); got != "(7, '1')" {
		t.Errorf("Image changed: %s", got)
	}
	if got := bitVec("1010").Image(); got != `"1010"` {
		t.Errorf("Image changed: %s", got)
	}
}

// TestImageOfScalars: on scalars ImageOf is exactly Image, so 'IMAGE
// of a scalar keeps its v1.0 output. (Access values have no 'IMAGE of
// their own; inside composites see TestImageOfNoHandles.)
func TestImageOfScalars(t *testing.T) {
	var vals []Value
	for _, x := range operands(11, 30) {
		vals = append(vals, Int(Integer64, x), Phys(Time, x))
	}
	for p := range int64(256) {
		vals = append(vals, Enum(Character, p))
	}
	vals = append(vals, Bool(true), Enum(SeverityLevel, 3),
		RealValue(Real, 1.5), RealValue(Real, -2e-7))
	for _, v := range vals {
		if ImageOf(v) != v.Image() {
			t.Fatalf("%s: %q != %q", v.T.Name, ImageOf(v), v.Image())
		}
	}
}

// randValue builds a random composite value of nested arrays and
// records over integers, bits and characters, with its type.
func randValue(r *env.SplitMix64, depth int) Value {
	k := r.Uint64() % 5
	if depth == 0 {
		k %= 3
	}
	switch k {
	case 0:
		return Int(Integer, int64(r.Uint64()%201)-100)
	case 1:
		return Enum(Bit, int64(r.Uint64()%2))
	case 2:
		// Printable, no space: the shape test looks for spaces.
		return Enum(Character, 33+int64(r.Uint64()%94))
	case 3:
		el := randValue(r, depth-1)
		n := int(r.Uint64() % 4)
		e := []Value{el}
		for range n {
			e = append(e, el)
		}
		at := &Type{Kind: KindArray, Name: "A",
			Index: []*Type{Integer}, Elem: el.T}
		l := int64(r.Uint64()%10) - 5
		return Array(at, []Range{{l, l + int64(len(e)) - 1, To}}, e)
	}
	n := 1 + int(r.Uint64()%3)
	rt := &Type{Kind: KindRecord, Name: "R"}
	var e []Value
	for i := range n {
		f := randValue(r, depth-1)
		rt.Fields = append(rt.Fields, Field{
			Name: string(rune('a' + i)), Type: f.T})
		e = append(e, f)
	}
	return Record(rt, e)
}

// TestImageOfShape: the image of a random composite is deterministic,
// has no spaces (no physical or space values are generated), and is an
// aggregate or a string literal.
func TestImageOfShape(t *testing.T) {
	r := &env.SplitMix64{State: 5}
	for range 500 {
		v := randValue(r, 3)
		img := ImageOf(v)
		if img != ImageOf(v) {
			t.Fatal("not deterministic")
		}
		if strings.Contains(img, " ") || strings.Contains(img, ", ") {
			t.Fatalf("space in %s", img)
		}
		if v.Kind().IsScalar() {
			continue
		}
		if img[0] != '(' && img[0] != '"' {
			t.Fatalf("composite image %s", img)
		}
	}
}

// TestImageAttr: T'IMAGE of a composite, also of an unconstrained
// array type, and O'IMAGE of an object prefix evaluate through the
// IR and charge one step per scalar.
func TestImageAttr(t *testing.T) {
	m := &Machine{}
	v := ints(intVecT, 0, 1, -2, 3)
	got, err := m.Eval(&Attr{Kind: AttrImage, Type: intVecT,
		Arg: &Const{V: v}})
	if err != nil || got.GoString() != "(1,-2,3)" {
		t.Fatalf("iarr'image = %q %v", got.GoString(), err)
	}
	got, err = m.Eval(&Attr{Kind: AttrImage, Type: recT,
		Arg: &Const{V: rec(7, 1)}})
	if err != nil || got.GoString() != "(7,'1')" {
		t.Fatalf("rec'image = %q %v", got.GoString(), err)
	}
	got, err = m.Eval(&Attr{Kind: AttrImage, Of: &Const{V: rec(7, 1)}})
	if err != nil || got.GoString() != "(7,'1')" {
		t.Fatalf("r'image = %q %v", got.GoString(), err)
	}
	got, err = m.Eval(&Attr{Kind: AttrImage, Type: BitVector,
		Arg: &Const{V: bitVec("1010")}})
	if err != nil || got.GoString() != `"1010"` || got.T != String {
		t.Fatalf("bit_vector'image = %q %v", got.GoString(), err)
	}
	// Other attributes of an unconstrained array type still fail.
	if _, err := m.Eval(&Attr{Kind: AttrLength,
		Type: intVecT}); CodeOf(err) != ErrCodeNotStatic {
		t.Fatalf("iarr'length: %v", err)
	}
	// 3 steps for the elements, 2 for the nodes: 5 fit, 4 do not.
	for _, n := range []int64{4, 5} {
		ok := n == 5
		mb := &Machine{Budget: NewBudget(n)}
		_, err := mb.Eval(&Attr{Kind: AttrImage, Type: intVecT,
			Arg: &Const{V: v}})
		if (err == nil) != ok {
			t.Fatalf("budget %d: %v", n, err)
		}
	}
}

// TestInferSubtype: the inferred subtype is fully constrained by the
// value, the value checks against it with its own bounds, inference is
// idempotent, and a fully constrained subtype is returned as is.
func TestInferSubtype(t *testing.T) {
	bv := bitVec("10110")
	s := InferSubtype(BitVector, bv)
	if !FullyConstrained(s) || s.Bounds[0] != (Range{4, 0, Downto}) ||
		s.BaseType() != BitVector || s.Bounds[0].Len() != 5 {
		t.Fatalf("bit_vector: %+v", s)
	}
	if InferSubtype(s, bv) != s {
		t.Fatal("constrained subtype not returned as is")
	}
	if InferSubtype(Integer, Int(Integer, 3)) != Integer {
		t.Fatal("scalar changed")
	}
	// Element constraint from the first element.
	vv := list(bvVecT, 0, bitVec("101"), bitVec("011"))
	s = InferSubtype(bvVecT, vv)
	if s.Bounds[0] != (Range{0, 1, To}) ||
		s.Elem.Bounds[0] != (Range{2, 0, Downto}) ||
		s.Elem.BaseType() != BitVector {
		t.Fatalf("element: %+v %+v", s, s.Elem)
	}
	// Partially constrained: bounds given, element open.
	pc := bvVecT.ArraySubtype(Range{1, 2, To})
	s = InferSubtype(pc, list(bvVecT, 1, bitVec("11"), bitVec("00")))
	if s.Bounds[0] != (Range{1, 2, To}) || s.Elem.Bounds[0].Len() != 2 {
		t.Fatalf("partial: %+v", s)
	}
	// Record fields, each from its own value.
	rv := Record(openRecT, []Value{Int(Integer, 1), bitVec("1"),
		Str("abc")})
	s = InferSubtype(openRecT, rv)
	if s.Fields[0].Type != Integer ||
		s.Fields[1].Type.Bounds[0].Len() != 1 ||
		s.Fields[2].Type.Bounds[0] != (Range{1, 3, To}) ||
		s.BaseType() != openRecT || openRecT.Fields[1].Type != BitVector {
		t.Fatalf("record: %+v", s)
	}
	// Null array value: outer bounds inferred, the open element stays.
	s = InferSubtype(bvVecT, Array(bvVecT, []Range{{0, -1, To}}, nil))
	if s.Bounds == nil || s.Elem != BitVector {
		t.Fatalf("null: %+v", s)
	}
	// Property over random shapes: inferring every level of an open
	// layout from a value of it gives a fully constrained subtype
	// that the value checks against, with the value's own bounds.
	r := &env.SplitMix64{State: 9}
	for range 300 {
		v := randValue(r, 3)
		open := openLayout(v.T)
		s := InferSubtype(open, v)
		if !FullyConstrained(s) && !hasNull(v) {
			t.Fatalf("not fully constrained: %s", ImageOf(v))
		}
		w, err := Check(v, s)
		if err != nil || !w.Equal(v) || !sameBounds(w, v) {
			t.Fatalf("check %s: %v", ImageOf(v), err)
		}
		if InferSubtype(s, v) != s && FullyConstrained(s) {
			t.Fatal("not idempotent")
		}
	}
}

// openLayout returns t with every array constraint opened, at every
// level.
func openLayout(t *Type) *Type {
	switch t.Kind {
	case KindArray:
		s := *t
		s.Bounds = nil
		s.Elem = openLayout(t.Elem)
		return &s
	case KindRecord:
		s := *t
		s.Fields = nil
		for _, f := range t.Fields {
			s.Fields = append(s.Fields,
				Field{Name: f.Name, Type: openLayout(f.Type)})
		}
		return &s
	}
	return t
}

func hasNull(v Value) bool {
	if v.Kind() == KindArray && len(v.E) == 0 {
		return true
	}
	for _, e := range v.E {
		if hasNull(e) {
			return true
		}
	}
	return false
}

func sameBounds(a, b Value) bool {
	if len(a.B) != len(b.B) || len(a.E) != len(b.E) {
		return false
	}
	for i := range a.B {
		if a.B[i] != b.B[i] {
			return false
		}
	}
	for i := range a.E {
		if !sameBounds(a.E[i], b.E[i]) {
			return false
		}
	}
	return true
}

// TestImageOfNoHandles: an access value inside a composite is null or
// access, never its handle, so 'IMAGE does not depend on allocation
// order.
func TestImageOfNoHandles(t *testing.T) {
	ptr := &Type{Kind: KindAccess, Name: "P", Elem: Integer}
	rt := &Type{Kind: KindRecord, Name: "R", Fields: []Field{
		{"a", Integer}, {"p", ptr}}}
	at := &Type{Kind: KindArray, Name: "PA", Index: []*Type{Natural},
		Elem: ptr}
	for _, h := range []int64{1, 7, 123456789} {
		r := Value{T: rt, E: []Value{Int(Integer, 3), {T: ptr, I: h}}}
		if got := ImageOf(r); got != "(3,access)" {
			t.Errorf("handle %d: %q, want (3,access)", h, got)
		}
		a := Array(at, []Range{{0, 1, To}}, []Value{{T: ptr},
			{T: ptr, I: h}})
		if got := ImageOf(a); got != "(null,access)" {
			t.Errorf("handle %d: %q, want (null,access)", h, got)
		}
	}
}
