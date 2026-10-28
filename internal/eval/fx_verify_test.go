package eval

// fx_verify_test.go: regression tests of the v1.1 Stage 3
// verification fixes in the evaluator (#18): 'VALUE of a floating
// type, and the VHDL-2008 MINIMUM and MAXIMUM of an array of a scalar
// type, which failed as evaluator gaps.

import (
	"math"
	"strconv"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// TestFxRealValue: 'VALUE of a floating type reads back every 'IMAGE
// and the decimal literal forms (underscores, exponents, a sign,
// spaces), within the subtype; Go's other float spellings are no
// values (ErrCodeValue, which analysis does not report), a literal
// beyond the type is out of range.
func TestFxRealValue(t *testing.T) {
	m := &Machine{}
	value := func(s string, typ *Type) (Value, error) {
		return m.Eval(&Attr{Kind: AttrValue, Type: typ,
			Arg: &Const{V: Str(s)}})
	}
	rng := &env.SplitMix64{State: 7}
	for range 300 {
		f := math.Float64frombits(rng.Uint64())
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		v := RealValue(Real, f)
		got, err := value(v.Image(), Real)
		if err != nil || got.F != f {
			t.Fatalf("real'value(%q) = %v %v, want %v", v.Image(), got.F,
				err, f)
		}
	}
	for s, want := range map[string]float64{
		"1.5": 1.5, " -2_0.25e1 ": -202.5, "+3.0E-2": 0.03,
		"1_000.0": 1000, "7": 7,
	} {
		got, err := value(s, Real)
		if err != nil || math.Abs(got.F-want) > 1e-12 {
			t.Errorf("real'value(%q) = %v %v, want %v", s, got.F, err,
				want)
		}
	}
	for _, s := range []string{"", "inf", "-Inf", "NaN", "0x1p3", "1.5x",
		"e5", "."} {
		if _, err := value(s, Real); CodeOf(err) != ErrCodeValue {
			t.Errorf("real'value(%q): %v, want no value", s, err)
		}
	}
	if _, err := value("1e400", Real); CodeOf(err) != ErrCodeRange {
		t.Errorf("real'value(1e400): %v, want a range error", err)
	}
	unit := Real.RealSubtype(RealRange{0, 1, To})
	if _, err := value("1.5", unit); CodeOf(err) != ErrCodeRange {
		t.Errorf("1.5 in 0.0 to 1.0: %v", err)
	}
}

// TestFxArrayExtremum: MINIMUM (MAXIMUM) of an array is one of its
// elements and not greater (less) than any; of a null array it is the
// element subtype's greatest (least) value (LRM 5.3.2.4); one step per
// element.
func TestFxArrayExtremum(t *testing.T) {
	elem := Integer.Subtype(Range{-50, 50, To})
	arr := &Type{Kind: KindArray, Name: "IV", Index: []*Type{Natural},
		Elem: elem}
	rng := &env.SplitMix64{State: 11}
	for n := range 40 {
		es := make([]Value, n%9)
		for i := range es {
			es[i] = Int(elem, int64(rng.Uint64()%101)-50)
		}
		v := Value{T: arr, E: es}
		for _, o := range []Op{OpMinimum, OpMaximum} {
			b := NewBudget(0)
			got, err := Apply(b, o, elem, v)
			if err != nil {
				t.Fatalf("%v of %d elements: %v", o, len(es), err)
			}
			if b.Used() < int64(len(es)) {
				t.Errorf("%v: %d steps for %d elements", o, b.Used(),
					len(es))
			}
			if len(es) == 0 {
				want := elem.Range.High()
				if o == OpMaximum {
					want = elem.Range.Low()
				}
				if got.I != want {
					t.Errorf("%v of a null array = %d, want %d", o,
						got.I, want)
				}
				continue
			}
			member := false
			for _, e := range es {
				member = member || e.I == got.I
				if o == OpMinimum && e.I < got.I ||
					o == OpMaximum && e.I > got.I {
					t.Errorf("%v = %d, but %d is an element", o, got.I,
						e.I)
				}
			}
			if !member {
				t.Errorf("%v = %d is no element", o, got.I)
			}
		}
	}
	rv := Value{T: &Type{Kind: KindArray, Name: "RV",
		Index: []*Type{Natural}, Elem: Real},
		E: []Value{RealValue(Real, 1.5), RealValue(Real, -2)}}
	lo, err := Apply(nil, OpMinimum, Real, rv)
	if err != nil || lo.F != -2 {
		t.Errorf("minimum of a real array = %v %v", lo.F, err)
	}
	s := Str("vhdl")
	hi, err := Apply(nil, OpMaximum, Character, s)
	if err != nil || hi.I != 'v' {
		t.Errorf("maximum of %q = %s %v", "vhdl",
			strconv.Quote(string(rune(hi.I))), err)
	}
}
