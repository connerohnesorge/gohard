package psl

import (
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/elab"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// stateSrc has one implication assert (a1), one cover (c1) and two
// asserts without an antecedent: a2 never fails, a3 fails at every
// tick where x and y are both high. The drive process
// is replaced by a Go stand-in (driver) that plays a word on x and y.
const stateSrc = `library ieee;
use ieee.std_logic_1164.all;
entity n is end entity;
architecture a of n is
  signal clk, x, y : std_logic := '0';
  default clock is rising_edge(clk);
begin
  drive : process
  begin
    clk <= '0'; x <= '0'; y <= '0';
    wait;
  end process;
  a1 : assert always (x = '1' -> next y = '1');
  c1 : cover {x = '1'; y = '1'};
  a2 : assert always (y = '0' or y = '1');
  a3 : assert always (x = '0' or y = '0');
end architecture;
`

// obs is what the driver reads at a falling edge, after the rising
// edge of tick i ran and before it clears (when it does).
type obs struct {
	failed          bool
	covers, asserts int
	errors          uint64
	coverAssert     bool
	epoch           uint64
	clearAfter      bool
}

// driver drives clk (period 10 ns, rising at 5 + 10i ns) and the
// letter w[i] on x, y half a period before rising edge i; at the
// falling edge after rising edge i it records the PSL state and, when
// clear[i], calls ClearPslState. It ends the run after the last tick.
// Every clone registers itself in reg, so the test finds the live copy
// of each kernel.
type driver struct {
	w     [][2]bool
	clear []bool
	clk   sim.SigID
	x, y  sim.SigID
	ty    *eval.Type
	calls int
	seen  []obs
	reg   *[]*driver
}

func (p *driver) Clone() sim.Code {
	q := *p
	q.seen = slices.Clone(p.seen)
	*p.reg = append(*p.reg, &q)
	return &q
}

func (p *driver) bit(b bool) eval.Value {
	if b {
		return lit(p.ty, "'1'")
	}
	return lit(p.ty, "'0'")
}

func (p *driver) Resume(c *sim.Ctx) (sim.Wait, error) {
	p.calls++
	n := p.calls - 1 // 0: time 0; then one call every 5 ns
	wait := sim.Wait{For: 5 * ns, HasFor: true}
	switch {
	case n%2 == 0: // 10i ns: drive w[i], clock low
		i := n / 2
		if i > 0 {
			// the falling edge after rising edge i-1
			o := obs{failed: c.PSLFailed(), covers: c.PSLCovered(false),
				asserts: c.PSLCovered(true), coverAssert: c.PSLCoverAssert(),
				errors: c.AssertCount(trace.Error), epoch: c.PSLEpoch(),
				clearAfter: p.clear[i-1]}
			p.seen = append(p.seen, o)
			if p.clear[i-1] {
				c.ClearPSL()
			}
			if i%3 == 0 {
				c.SetPSLCoverAssert(i%2 == 0)
			}
		}
		if i == len(p.w) {
			c.Stop(0, true, sim.SrcRef{})
			return sim.Wait{}, nil
		}
		if err := assign(c, p.clk, p.bit(false), 0); err != nil {
			return sim.Wait{}, err
		}
		if err := assign(c, p.x, p.bit(p.w[i][0]), 0); err != nil {
			return sim.Wait{}, err
		}
		return wait, assign(c, p.y, p.bit(p.w[i][1]), 0)
	}
	return wait, assign(c, p.clk, p.bit(true), 0) // rising edge
}

// runState runs stateSrc on the word w with ClearPslState after the
// ticks in clear; at cut > 0 it clones the kernel after cut kernel
// slices and returns the clone's observations too.
func runState(t *testing.T, w [][2]bool, clear []bool, cut int,
) (*sim.Kernel, *driver, *driver) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = "2008"
	r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
		Files: []sema.Source{{Path: "n.vhd", Text: []byte(stateSrc)}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("analysis: %s %s", d.Code, d.Message)
		}
	}
	var reg []*driver
	stub := func(d *elab.Design, p *elab.Proc) (sim.Code, error) {
		if p.Path != "n.drive" {
			return nil, nil
		}
		clk, ty := stdLogic(t, d, "n.clk")
		x, _ := stdLogic(t, d, "n.x")
		y, _ := stdLogic(t, d, "n.y")
		return &driver{w: w, clear: clear, clk: clk, x: x, y: y, ty: ty,
			reg: &reg}, nil
	}
	d, ds := elab.Elaborate(r, elab.Options{Top: "n", Code: Wrap(stub)})
	if len(ds) > 0 || len(d.Missing) > 0 {
		t.Fatalf("elaborate: %v missing %v", ds, d.Missing)
	}
	k, err := sim.New(d.Model, sim.Options{StopLevel: trace.Failure})
	if err != nil {
		t.Fatal(err)
	}
	// reg[0] is k's driver (sim.New clones the model's code), reg[1]
	// the fork's.
	var fork *sim.Kernel
	if cut > 0 {
		k.Run(cut)
		fork = k.Clone()
		fork.RunAll(64, nil)
		if fork.Err() != nil {
			t.Fatal(fork.Err())
		}
	}
	k.RunAll(64, nil)
	if k.Err() != nil {
		t.Fatal(k.Err())
	}
	if fork == nil {
		return k, reg[0], nil
	}
	return k, reg[0], reg[1]
}

// refState is the reference semantics of stateSrc's directives on w
// with ClearPslState after the ticks in clear: the observation after
// each tick. An epoch starts at the tick after a clear with no state:
// a1 (x -> next y) fails at tick j when x held at j-1 in the epoch and
// y does not hold at j, and is covered once x held in the epoch; c1
// is covered once x held at j-1 and y at j in the epoch; a2 and a3
// are covered at the first tick of the epoch, and a3 fails at every
// tick where x and y hold.
func refState(w [][2]bool, clear []bool) []obs {
	var out []obs
	start := 0
	var failed bool
	var errors uint64
	var epoch uint64
	coverAssert := false
	for i := range w {
		a1cov, c1cov := false, false
		for j := start; j <= i; j++ {
			a1cov = a1cov || w[j][0]
			if j > start && w[j-1][0] {
				c1cov = c1cov || w[j][1]
				if !w[j][1] && j == i {
					failed = true
					errors++
				}
			}
		}
		if w[i][0] && w[i][1] {
			failed = true
			errors++
		}
		o := obs{failed: failed, errors: errors, epoch: epoch,
			clearAfter: clear[i], coverAssert: coverAssert}
		o.asserts = 2 // a2 and a3, covered at the epoch's first tick
		if a1cov {
			o.asserts++
		}
		if c1cov {
			o.covers = 1
		}
		out = append(out, o)
		if clear[i] {
			start, failed = i+1, false
			epoch++
		}
		if (i+1)%3 == 0 {
			coverAssert = (i+1)%2 == 0
		}
	}
	return out
}

// TestPSLState (#18): on random words with ClearPslState at random
// ticks, PslAssertFailed and the covered cover and assert directives
// agree with the reference semantics after every tick; ClearPslState
// resets them (and every runner's obligations and matches) but not
// the std.env assert counters, which count every enabled PSL
// violation; SetPslCoverAssert is kept across ClearPslState.
func TestPSLState(t *testing.T) {
	r := &rng{env.SplitMix64{State: 18}}
	for iter := range 60 {
		n := 2 + r.n(14)
		w := make([][2]bool, n)
		clear := make([]bool, n)
		for i := range n {
			w[i] = [2]bool{r.n(2) == 1, r.n(2) == 1}
			clear[i] = r.n(4) == 0
		}
		_, drv, _ := runState(t, w, clear, 0)
		want := refState(w, clear)
		if !slices.Equal(drv.seen, want) {
			t.Fatalf("iter %d w %v clear %v:\n got %+v\nwant %+v", iter,
				w, clear, drv.seen, want)
		}
	}
}

// TestPSLStateClone: a kernel cloned mid-run continues with the same
// PSL state, epoch and runner state as the original.
func TestPSLStateClone(t *testing.T) {
	r := &rng{env.SplitMix64{State: 27}}
	for iter := range 20 {
		n := 4 + r.n(10)
		w := make([][2]bool, n)
		clear := make([]bool, n)
		for i := range n {
			w[i] = [2]bool{r.n(2) == 1, r.n(2) == 1}
			clear[i] = r.n(3) == 0
		}
		_, a, b := runState(t, w, clear, 1+r.n(3*n))
		if !slices.Equal(a.seen, b.seen) ||
			!slices.Equal(a.seen, refState(w, clear)) {
			t.Fatalf("iter %d: original %+v\nclone %+v", iter, a.seen,
				b.seen)
		}
	}
}

// TestPSLStateFinish: ClearPslState reaches the end-of-run cover
// report too: a cover hit only before the last clear is a miss.
func TestPSLStateFinish(t *testing.T) {
	w := [][2]bool{{true, false}, {false, true}, {false, false}}
	for _, clr := range []bool{false, true} {
		k, _, _ := runState(t, w, []bool{false, clr, false}, 0)
		var codes []diag.Code
		for _, d := range k.Diagnostics() {
			codes = append(codes, d.Code)
		}
		want := diag.SimCoverHit
		if clr {
			want = diag.SimCoverMiss
		}
		if !slices.Contains(codes, want) {
			t.Errorf("clear %v: diagnostics %v, want %s", clr, codes, want)
		}
	}
}
