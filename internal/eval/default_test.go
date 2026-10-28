package eval

import (
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// TestDefault: the default value of every scalar subtype is its 'LEFT
// and belongs to the subtype; composites hold the default of every
// element, one step per element; unconstrained arrays have none.
func TestDefault(t *testing.T) {
	rng := &env.SplitMix64{State: 3}
	for range 200 {
		l := int64(rng.Uint64()%1000) - 500
		r := int64(rng.Uint64()%1000) - 500
		dir := To
		if rng.Uint64()%2 == 1 {
			dir = Downto
		}
		st := Integer.Subtype(Range{l, r, dir})
		v, err := Default(nil, st)
		if err != nil || v.I != l {
			t.Fatalf("default of %s = %v, %v", st.Range, v.I, err)
		}
		if !st.Range.Null() {
			if _, err := Check(v, st); err != nil {
				t.Fatalf("default of %s is not in it: %v", st.Range, err)
			}
		}
	}
	bv := BitVector.ArraySubtype(Range{7, 0, Downto})
	b := NewBudget(0)
	v, err := Default(b, bv)
	if err != nil || len(v.E) != 8 || v.B[0] != (Range{7, 0, Downto}) ||
		b.Used() != 8 {
		t.Fatalf("bit_vector(7 downto 0): %v %v %v steps %d", v.Image(),
			v.B, err, b.Used())
	}
	for _, e := range v.E {
		if e.I != 0 {
			t.Fatal("bit default is not '0'")
		}
	}
	rec := &Type{Kind: KindRecord, Name: "R", Fields: []Field{
		{"a", Integer}, {"b", Boolean}, {"c", bv}}}
	rv, err := Default(nil, rec)
	if err != nil || rv.Image() != `(-2147483648, false, "00000000")` {
		t.Fatalf("record default %s %v", rv.Image(), err)
	}
	if _, err := Default(nil, BitVector); CodeOf(err) != ErrCodeNotStatic {
		t.Fatalf("unconstrained default: %v", err)
	}
	huge := BitVector.ArraySubtype(Range{0, 1 << 30, To},
		Range{0, 1 << 30, To})
	if _, err := Default(nil, huge); CodeOf(err) != ErrCodeUnsupported {
		t.Fatalf("huge default: %v", err)
	}
	if _, err := Default(NewBudget(5), bv); CodeOf(err) != ErrCodeBudget {
		t.Fatalf("budget: %v", err)
	}
}

// TestNamedAggregateBounds: a named aggregate without others has the
// bounds of its choices and converts to a constrained context of the
// same length (LRM 9.3.3.3); with others it takes the context's
// bounds.
func TestNamedAggregateBounds(t *testing.T) {
	up := &Type{Kind: KindArray, Name: "V", Index: []*Type{Integer},
		Elem: Integer}
	ctx := up.ArraySubtype(Range{7, 8, To})
	one := &Const{V: Int(Integer, 1)}
	two := &Const{V: Int(Integer, 2)}
	named := &Aggregate{T: ctx, Assocs: []Assoc{
		{Choices: []Choice{{Kind: ChoiceIndex, Index: one}}, Value: one},
		{Choices: []Choice{{Kind: ChoiceIndex, Index: two}}, Value: two},
	}}
	m := &Machine{Budget: NewBudget(0)}
	v, err := m.Eval(named)
	if err != nil || v.B[0] != (Range{7, 8, To}) || v.Image() != "(1, 2)" {
		t.Fatalf("(1 => 1, 2 => 2) as (7 to 8): %v %v %v", v.Image(),
			v.B, err)
	}
	others := &Aggregate{T: ctx, Assocs: []Assoc{
		{Choices: []Choice{{Kind: ChoiceIndex, Index: &Const{
			V: Int(Integer, 7)}}}, Value: one},
		{Choices: []Choice{{Kind: ChoiceOthers}}, Value: two},
	}}
	v, err = m.Eval(others)
	if err != nil || v.Image() != "(1, 2)" {
		t.Fatalf("(7 => 1, others => 2): %v %v", v.Image(), err)
	}
	bad := &Aggregate{T: up.ArraySubtype(Range{1, 3, To}),
		Assocs: named.Assocs}
	if _, err := m.Eval(bad); CodeOf(err) != ErrCodeLength {
		t.Fatalf("(1 => 1, 2 => 2) as (1 to 3): %v", err)
	}
}

// TestSliceError: slices leaving the array or in the wrong direction
// are index errors naming both ranges.
func TestSliceError(t *testing.T) {
	a := Array(BitVector, []Range{{7, 0, Downto}},
		make([]Value, 8))
	m := &Machine{Budget: NewBudget(0)}
	for _, c := range []struct {
		r    RangeExpr
		want string
	}{
		{RangeExpr{Left: &Const{V: Int(Integer, 9)},
			Right: &Const{V: Int(Integer, 4)}, Dir: Downto},
			"slice 9 downto 4 is out of range 7 downto 0"},
		{RangeExpr{Left: &Const{V: Int(Integer, 1)},
			Right: &Const{V: Int(Integer, 4)}, Dir: To},
			"slice direction to differs from the array's downto"},
	} {
		_, err := m.Eval(&Slice{X: &Const{V: a}, R: c.r})
		if CodeOf(err) != ErrCodeIndex || err.Error() != c.want {
			t.Errorf("got %v, want %s", err, c.want)
		}
	}
}
