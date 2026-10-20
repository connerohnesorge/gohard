package sim

import (
	"testing"

	"github.com/connerohnesorge/gohard/internal/eval"
)

// stdLogicTable is the IEEE 1164 resolution table (LRM-companion
// std_logic_1164 body), as data: row and column order U X 0 1 Z W L H -.
var stdLogicTable = [9]string{
	"UUUUUUUUU", // U
	"UXXXXXXXX", // X
	"UX0X0000X", // 0
	"UXX11111X", // 1
	"UX01ZWLHX", // Z
	"UX01WWWWX", // W
	"UX01LWLWX", // L
	"UX01HWWHX", // H
	"UXXXXXXXX", // -
}

const logicChars = "UX01ZWLH-"

func tableRes(a, b int64) int64 {
	c := stdLogicTable[a][b]
	for i := range logicChars {
		if logicChars[i] == c {
			return int64(i)
		}
	}
	panic("bad table")
}

// goResolved is ieee.std_logic_1164.resolved written in Go over the
// table: one driver gives its value, more fold from 'Z'.
var goResolved = ResolverFunc(func(b *eval.Budget, d []eval.Value) (
	eval.Value, error,
) {
	if err := b.Step(int64(len(d))); err != nil {
		return eval.Value{}, err
	}
	if len(d) == 1 {
		return d[0], nil
	}
	r := int64(lZ)
	for _, v := range d {
		r = tableRes(r, v.I)
	}
	return eval.Value{T: tLogic, I: r}, nil
})

// TestTableLaws checks the table is commutative and associative, with
// Z as identity and U absorbing.
func TestTableLaws(t *testing.T) {
	for a := range int64(9) {
		if tableRes(a, lZ) != a && a != lD {
			t.Errorf("Z is not an identity for %c", logicChars[a])
		}
		if tableRes(a, lU) != lU {
			t.Errorf("U does not absorb %c", logicChars[a])
		}
		for b := range int64(9) {
			if tableRes(a, b) != tableRes(b, a) {
				t.Errorf("not commutative at %c %c", logicChars[a],
					logicChars[b])
			}
			for c := range int64(9) {
				l := tableRes(tableRes(a, b), c)
				r := tableRes(a, tableRes(b, c))
				if l != r {
					t.Errorf("not associative at %c %c %c",
						logicChars[a], logicChars[b], logicChars[c])
				}
			}
		}
	}
}

// TestResolutionOrder checks that a resolved net's value does not
// depend on driver order: every permutation of generated driver sets
// resolves alike, in the resolver and through the kernel.
func TestResolutionOrder(t *testing.T) {
	r := newRNG(12)
	for range 500 {
		n := 1 + r.IntN(5)
		vals := make([]eval.Value, n)
		for i := range vals {
			vals[i] = eval.Value{T: tLogic, I: int64(r.IntN(9))}
		}
		want, _ := goResolved(nil, vals)
		for range 5 {
			p := append([]eval.Value(nil), vals...)
			r.Shuffle(len(p), func(i, j int) { p[i], p[j] = p[j], p[i] })
			got, _ := goResolved(nil, p)
			if got.I != want.I {
				t.Fatalf("order changes resolution: %v vs %v", vals, p)
			}
		}
		// The same drivers through the kernel, as n processes, in
		// declaration order and shuffled orders.
		for seed := range uint64(3) {
			m := &Model{Testbench: "tb"}
			s := m.AddSignal(Signal{Path: "tb.s", Type: tLogic,
				Init: eval.Value{T: tLogic, I: lU}, Resolve: goResolved})
			for i, v := range vals {
				m.Processes = append(m.Processes, Process{
					Path:    "tb.p" + string(rune('a'+i)),
					Drivers: []Target{Whole(s)},
					Code: &prog{sigT: []*eval.Type{tLogic}, ops: []op{
						{kind: opAssign, sig: s, vals: []int64{v.I},
							afters: []int64{1}},
						{kind: opWait},
					}}})
			}
			k := mustKernel(t, m, Options{Order: seedOrder(seed)})
			k.RunAll(100, nil)
			if got := k.value(s).I; got != want.I {
				t.Fatalf("kernel resolution %d, want %d", got, want.I)
			}
		}
	}
}
