package eval

import "testing"

// TestULogicMatching: "?=" of STD_ULOGIC is commutative, '-' matches
// everything, L/H match 0/1, and "?/=" is its "not"; the ordering
// operators agree with To_X01 ordering on '0'/'1'/'L'/'H'.
func TestULogicMatching(t *testing.T) {
	lits := []string{"'U'", "'X'", "'0'", "'1'", "'Z'", "'W'", "'L'",
		"'H'", "'-'"}
	ul := &Type{Kind: KindEnum, Name: "STD_ULOGIC", Literals: lits,
		Range: Range{Left: 0, Right: 8, Dir: To}}
	b := NewBudget(0)
	v := func(i int64) Value { return Enum(ul, i) }
	x01 := map[int64]int64{2: 0, 3: 1, 6: 0, 7: 1}
	for a := range int64(9) {
		for c := range int64(9) {
			eq, err := Apply(b, OpMatchEq, ul, v(a), v(c))
			if err != nil {
				t.Fatal(err)
			}
			eq2, _ := Apply(b, OpMatchEq, ul, v(c), v(a))
			ne, _ := Apply(b, OpMatchNe, ul, v(a), v(c))
			if eq.I != eq2.I || uNot(eq.I) != ne.I {
				t.Fatalf("%s ?= %s", lits[a], lits[c])
			}
			if (a == 8 || c == 8) && eq.I != u1 {
				t.Fatalf("%s ?= %s is not '1'", lits[a], lits[c])
			}
			xa, oka := x01[a]
			xc, okc := x01[c]
			if !oka || !okc {
				continue
			}
			if want := map[bool]int64{true: u1, false: u0}[xa == xc]; eq.I != want {
				t.Fatalf("%s ?= %s = %s", lits[a], lits[c], lits[eq.I])
			}
			lt, err := Apply(b, OpMatchLt, ul, v(a), v(c))
			if err != nil {
				t.Fatal(err)
			}
			ge, _ := Apply(b, OpMatchGe, ul, v(a), v(c))
			if want := map[bool]int64{true: u1, false: u0}[xa < xc]; lt.I != want ||
				ge.I != uNot(want) {
				t.Fatalf("%s ?< %s = %s", lits[a], lits[c], lits[lt.I])
			}
		}
	}
	arr := &Type{Kind: KindArray, Name: "STD_ULOGIC_VECTOR", Elem: ul}
	r := []Range{{Left: 1, Right: 0, Dir: Downto}}
	l := Array(arr, r, []Value{v(3), v(6)}) // "1L"
	h := Array(arr, r, []Value{v(7), v(2)}) // "H0"
	if eq, err := Apply(b, OpMatchEq, ul, l, h); err != nil || eq.I != u1 {
		t.Fatalf(`"1L" ?= "H0": %v %v`, eq, err)
	}
}
