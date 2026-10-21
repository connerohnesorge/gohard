package sim

import (
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/eval"
)

// warmRes is a pure resolver with a warm-up cost: the first call with
// a given single driver value costs extra steps (like an evaluator
// filling its constants), later ones a fixed per-value count, except
// value 3 whose calls cost one step less each time until they reach
// the base cost (a warm-up over several calls). calls counts its
// invocations.
type warmRes struct {
	seen  map[int64]bool
	calls int
	cold  int64
}

func (r *warmRes) Memoizable() {}

func (r *warmRes) Resolve(b *eval.Budget, d []eval.Value) (eval.Value,
	error,
) {
	r.calls++
	steps := int64(len(d))
	if len(d) == 1 {
		v := d[0].I
		steps += v
		if !r.seen[v] {
			r.seen[v] = true
			steps += 5
		}
		if v == 3 {
			steps += max(0, 4-r.cold)
			r.cold++
		}
	}
	if err := b.Step(steps); err != nil {
		return eval.Value{}, err
	}
	if len(d) == 1 {
		return eval.Value{T: tLogic, I: (d[0].I*7 + 1) % 9}, nil
	}
	x := int64(lZ)
	for _, v := range d {
		x = tableRes(x, v.I)
	}
	return eval.Value{T: tLogic, I: x}, nil
}

// cacheModel is a design of memories of std_logic with random initial
// values, resolved by res: one process drives some of them whole, two
// drive others, and some are not driven.
func cacheModel(seed uint64, res func() Resolver) *Model {
	r := newRNG(seed)
	m := &Model{Testbench: "tb"}
	mem := &eval.Type{Kind: eval.KindArray, Name: "mem"}
	var procs [2][]Target
	for i := range 6 {
		n := 1 + r.IntN(40)
		init := eval.Value{T: mem}
		for range n {
			init.E = append(init.E,
				eval.Value{T: tLogic, I: int64(r.IntN(9))})
		}
		s := m.AddSignal(Signal{Path: "tb.m" + itoa(int64(i)),
			Type: mem, Init: init, Resolve: res()})
		switch r.IntN(3) {
		case 0:
			procs[0] = append(procs[0], Whole(s))
		case 1:
			procs[0] = append(procs[0], Whole(s))
			procs[1] = append(procs[1], Whole(s))
		}
	}
	for i, ds := range procs {
		m.Processes = append(m.Processes, Process{
			Path: "tb.p" + itoa(int64(i)), Drivers: ds,
			Code: &fn{f: func(*Ctx, int) (Wait, error) {
				return forever()
			}}})
	}
	return m
}

// The initial resolution through the cache gives every net the value
// and the kernel the step count of calling the resolver for every net,
// at every step budget (the run ends at the same point with the same
// diagnostics), while calling a Memoizable resolver less often.
func TestInitCacheMatchesUncached(t *testing.T) {
	fewer := false
	for seed := range uint64(40) {
		w := &warmRes{seen: map[int64]bool{}}
		pw := &warmRes{seen: map[int64]bool{}}
		memo := cacheModel(seed, func() Resolver { return w })
		// The twin calls the same logic through a ResolverFunc,
		// which is not Memoizable: every net is resolved.
		plain := cacheModel(seed, func() Resolver {
			return ResolverFunc(pw.Resolve)
		})
		for _, lim := range []int64{0, 5, 40, 120, 300, 1 << 20} {
			*w = warmRes{seen: map[int64]bool{}}
			*pw = warmRes{seen: map[int64]bool{}}
			a := mustKernel(t, memo, Options{StepBudget: lim})
			b := mustKernel(t, plain, Options{StepBudget: lim})
			a.Run(1)
			b.Run(1)
			if a.Steps() != b.Steps() || a.Status() != b.Status() {
				t.Fatalf("seed %d limit %d: cached %d steps %v, "+
					"uncached %d %v", seed, lim, a.Steps(), a.Status(),
					b.Steps(), b.Status())
			}
			if !slices.Equal(codes(a), codes(b)) {
				t.Fatalf("seed %d limit %d: diagnostics %v vs %v", seed,
					lim, codes(a), codes(b))
			}
			for i := range a.nets {
				x, y := a.nets[i], b.nets[i]
				if !x.cur.Equal(y.cur) || !x.last.Equal(y.last) {
					t.Fatalf("seed %d limit %d: net %d is %v, want %v",
						seed, lim, i, x.cur, y.cur)
				}
			}
			fewer = fewer || w.calls < pw.calls
		}
	}
	if !fewer {
		t.Error("the cache never saved a resolver call")
	}
}

// A clone shares the net tables it never writes; a driver created at
// run time in one kernel (an assignment to a target the process did
// not declare) is not seen by the other.
func TestCloneNewDriverIsolated(t *testing.T) {
	m := &Model{Testbench: "tb"}
	s := m.AddSignal(Signal{Path: "tb.s", Type: tLogic,
		Init: eval.Value{T: tLogic, I: lU}, Resolve: goResolved})
	m.Processes = append(m.Processes,
		Process{Path: "tb.a", Drivers: []Target{Whole(s)},
			Code: &fn{f: func(*Ctx, int) (Wait, error) {
				return forever()
			}}},
		Process{Path: "tb.b", Code: &fn{f: func(c *Ctx, n int) (Wait,
			error,
		) {
			if n == 0 {
				return waitFor(1)
			}
			err := c.Assign(Whole(s), []WaveElem{{Value: eval.Value{
				T: tLogic, I: l1}, After: 0}}, Mechanism{})
			return Wait{}, err
		}}})
	k := mustKernel(t, m, Options{})
	k.Run(1)
	c := k.Clone()
	c.RunAll(10, nil)
	if got := len(c.nets[0].drivers); got != 2 {
		t.Fatalf("clone has %d drivers, want 2", got)
	}
	if got := len(k.nets[0].drivers); got != 1 {
		t.Fatalf("original has %d drivers after the clone ran, want 1",
			got)
	}
	k.RunAll(10, nil)
	if c.value(s).I != k.value(s).I || len(k.nets[0].drivers) != 2 {
		t.Fatalf("original and clone differ: %v vs %v", k.value(s),
			c.value(s))
	}
}
