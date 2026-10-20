package sim

import (
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
)

// reverseOrder is a non-identity Order: every in-delta batch runs in
// descending declaration order.
type reverseOrder struct{}

func (reverseOrder) Permute(_ uint64, n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = n - 1 - i
	}
	return p
}

type resumed struct {
	now   Time
	delta uint32
	proc  int32
}

// orderDesign builds a design where processes woken by a timeout and
// by an event on s resume in the same delta: a clock process toggles s
// every 3 fs and the others wait on s or for 3 or 6 fs, in a generated
// mix and declaration order. Every resume is appended to log.
func orderDesign(r *rng, log *[]resumed) *Model {
	m := &Model{Testbench: "tb"}
	s := intSig(m, "s", 0)
	n := 2 + r.IntN(6)
	clk := r.IntN(n)
	for i := range n {
		idx := int32(i)
		if i == clk {
			m.Processes = append(m.Processes, Process{
				Path: "tb.clk", Drivers: []Target{Whole(s)},
				Code: &fn{f: func(c *Ctx, k int) (Wait, error) {
					*log = append(*log, resumed{c.Now(), c.Delta(), idx})
					return Wait{On: nil, For: 3, HasFor: true},
						assign(c, s, int64(k%2), 3)
				}}})
			continue
		}
		onEvent := r.IntN(2) == 0
		d := int64(3 * (1 + r.IntN(2)))
		m.Processes = append(m.Processes, Process{
			Path: "tb.p" + string(rune('a'+i)),
			Code: &fn{f: func(c *Ctx, _ int) (Wait, error) {
				*log = append(*log, resumed{c.Now(), c.Delta(), idx})
				if onEvent {
					return Wait{On: []SigID{s}}, nil
				}
				return waitFor(d)
			}}})
	}
	return m
}

// TestInDeltaOrder: at seed 0 the processes of a delta resume in
// ascending declaration order, whatever woke them; with an Order they
// resume in that permutation of the sorted set.
func TestInDeltaOrder(t *testing.T) {
	r := newRNG(7)
	for i := range 300 {
		for _, o := range []env.Order{nil, reverseOrder{}} {
			var log []resumed
			m := orderDesign(r, &log)
			k := mustKernel(t, m, Options{Order: o, TimeBudget: 40})
			k.RunAll(16, nil)
			if k.Err() != nil {
				t.Fatal(k.Err())
			}
			for j := 0; j < len(log); {
				e := j
				for e < len(log) && log[e].now == log[j].now &&
					log[e].delta == log[j].delta {
					e++
				}
				var ps []int32
				for _, x := range log[j:e] {
					ps = append(ps, x.proc)
				}
				want := slices.Sorted(slices.Values(ps))
				if o != nil {
					slices.Reverse(want)
				}
				if !slices.Equal(ps, want) {
					t.Fatalf("design %d order %T at %d+%d: %v, want %v",
						i, o, log[j].now, log[j].delta, ps, want)
				}
				j = e
			}
		}
	}
}
