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
