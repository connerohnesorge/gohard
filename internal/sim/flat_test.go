package sim

import (
	"testing"

	"github.com/connerohnesorge/gohard/internal/eval"
)

// leaves and leafAt step over an array's elements by the element
// leaf count (every element has the element subtype), and agree with
// flatten on arrays of records of arrays.
func TestLeafAtArray(t *testing.T) {
	arr := &eval.Type{Kind: eval.KindArray, Name: "pair"}
	rec := &eval.Type{Kind: eval.KindRecord, Name: "r"}
	mem := &eval.Type{Kind: eval.KindArray, Name: "mem"}
	var els []eval.Value
	n := int64(0)
	for range 5 {
		r := eval.Value{T: rec, E: []eval.Value{
			{T: eval.Integer, I: n},
			{T: arr, E: []eval.Value{{T: eval.Integer, I: n + 1},
				{T: eval.Integer, I: n + 2}}},
		}}
		els = append(els, r)
		n += 3
	}
	v := eval.Value{T: mem, E: els}
	flat := flatten(v, nil)
	if leaves(v) != len(flat) || len(flat) != 15 {
		t.Fatalf("leaves = %d, flatten = %d, want 15", leaves(v), len(flat))
	}
	for i := range flat {
		got, ok := leafAt(v, i)
		if !ok || got.I != int64(i) {
			t.Errorf("leafAt(%d) = %d, %v", i, got.I, ok)
		}
	}
	if _, ok := leafAt(v, 15); ok {
		t.Error("leafAt past the end succeeded")
	}
}

// genValue builds a pseudo-random composite of depth at most d:
// arrays of same-shaped elements (sometimes empty), records of mixed
// fields, scalar leaves numbered in order by *next.
func genValue(seed *uint64, d int, next *int64) eval.Value {
	r := func(n uint64) uint64 {
		*seed = *seed*6364136223846793005 + 1442695040888963407
		return (*seed >> 33) % n
	}
	if d == 0 || r(4) == 0 {
		*next++
		return eval.Value{T: eval.Integer, I: *next}
	}
	if r(2) == 0 {
		arr := &eval.Type{Kind: eval.KindArray, Name: "a"}
		n := int(r(4))
		s0 := *seed
		v := eval.Value{T: arr}
		for range n {
			s := s0 // every element has the same shape
			v.E = append(v.E, genValue(&s, d-1, next))
			*seed = s
		}
		return v
	}
	rec := &eval.Type{Kind: eval.KindRecord, Name: "r"}
	v := eval.Value{T: rec}
	for range 1 + int(r(3)) {
		v.E = append(v.E, genValue(seed, d-1, next))
	}
	return v
}

// eachLeaf(v, lo, hi) visits leafAt(v, lo..hi-1) for every range of
// every generated value, and flatten for the whole range.
func TestEachLeafMatchesLeafAt(t *testing.T) {
	for s := range uint64(300) {
		seed, next := s, int64(0)
		v := genValue(&seed, 4, &next)
		n := leaves(v)
		flat := flatten(v, nil)
		if len(flat) != n {
			t.Fatalf("seed %d: flatten %d leaves, leaves %d", s,
				len(flat), n)
		}
		for lo := range n + 1 {
			for hi := lo; hi <= n; hi++ {
				var got []eval.Value
				eachLeaf(v, lo, hi, func(l eval.Value) {
					got = append(got, l)
				})
				if len(got) != hi-lo {
					t.Fatalf("seed %d [%d,%d): %d leaves", s, lo, hi,
						len(got))
				}
				for j, g := range got {
					w, ok := leafAt(v, lo+j)
					if !ok || g.I != w.I || g.I != flat[lo+j].I {
						t.Fatalf("seed %d [%d,%d) leaf %d = %d, "+
							"want %d", s, lo, hi, lo+j, g.I, w.I)
					}
				}
			}
		}
	}
}
