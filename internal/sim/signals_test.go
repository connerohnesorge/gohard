package sim

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// TestForceModes checks LRM 10.5.2.1: a force of mode out replaces the
// driving value, a force of mode in the effective value and hides a
// force out until released, and a release goes back to the driving
// value without a transaction on any driver.
func TestForceModes(t *testing.T) {
	m := &Model{Testbench: "tb"}
	s := intSig(m, "s", 0)
	m.Processes = []Process{
		{Path: "tb.drv", Drivers: []Target{Whole(s)},
			Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
				if n == 0 {
					return forever0(assign(c, s, 4, 0))
				}
				return forever()
			}}},
		{Path: "tb.f", Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
			step := Wait{For: 10, HasFor: true}
			switch n {
			case 0:
				return step, nil
			case 1: // 10: force out 7
				return step, c.ForceMode(Whole(s), iv(7), ForceOut)
			case 2: // 20: force in 9 hides the force out
				return step, c.ForceMode(Whole(s), iv(9), ForceIn)
			case 3: // 30: release in: back to the forced driving 7
				if !c.Forced(s) {
					t.Errorf("s is not forced at 30")
				}
				return step, c.ReleaseMode(Whole(s), ForceIn)
			case 4: // 40: release out: back to the driver's 4
				return step, c.ReleaseMode(Whole(s), ForceOut)
			case 5: // 50: releasing again does nothing
				if c.Forced(s) {
					t.Errorf("s is still forced at 50")
				}
				return forever0(c.ReleaseMode(Whole(s), ForceDefault))
			}
			return forever()
		}}},
	}
	_, r := run(t, m, Options{})
	wantLines(t, r, "0 0 tb.s 0", "0 1 tb.s 4", "10 1 tb.s 7",
		"20 1 tb.s 9", "30 1 tb.s 7", "40 1 tb.s 4")
}

// TestDefaultForceMode checks the LRM default: in for signals and in
// ports, out for out, inout and buffer ports.
func TestDefaultForceMode(t *testing.T) {
	for d, want := range map[trace.Dir]ForceMode{
		0: ForceIn, trace.Internal: ForceIn, trace.In: ForceIn,
		trace.Out: ForceOut, trace.Inout: ForceOut,
		trace.Buffer: ForceOut,
	} {
		if got := DefaultForceMode(&Signal{Dir: d}); got != want {
			t.Errorf("dir %v: %v, want %v", d, got, want)
		}
	}
}

// TestForcePortShared checks a force on a collapsed port reaches its
// actual: they are one net.
func TestForcePortShared(t *testing.T) {
	m := &Model{Testbench: "tb"}
	a := intSig(m, "a", 0)
	p := m.AddSignal(Signal{Path: "tb.u.p", Type: tInt, Init: iv(0),
		Dir: trace.Out, Collapsed: true, Of: a})
	m.Processes = []Process{{Path: "tb.f",
		Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
			if n == 0 {
				return Wait{For: 5, HasFor: true},
					c.ForceMode(Whole(p), iv(3), ForceDefault)
			}
			if c.Value(a).I != 3 || c.Value(p).I != 3 {
				t.Errorf("a=%d p=%d", c.Value(a).I, c.Value(p).I)
			}
			return forever()
		}}}}
	run(t, m, Options{})
}

// racy builds a design whose trace depends on in-delta order: w sets a
// shared variable that r reads in the same delta (a shared variable
// race). quiet runs in the same delta and touches nothing.
func racy() *Model {
	m := &Model{Testbench: "tb"}
	s := intSig(m, "s", 0)
	shared := new(int64)
	m.Processes = []Process{
		{Path: "tb.quiet", Code: &fn{f: func(*Ctx, int) (Wait, error) {
			return forever()
		}}},
		{Path: "tb.w", Code: &fn{f: func(*Ctx, int) (Wait, error) {
			*shared = 1
			return forever()
		}}},
		{Path: "tb.r", Drivers: []Target{Whole(s)},
			Code: &fn{f: func(c *Ctx, _ int) (Wait, error) {
				return forever0(assign(c, s, *shared+5, 0))
			}}},
	}
	return m
}

func raceRun(t *testing.T, mk func() *Model, seed uint64) RaceRun {
	t.Helper()
	m := mk()
	var log OrderLog
	rec := trace.NewRecorder(nil)
	k := mustKernel(t, m, Options{Seed: seed, Order: seedOrder(seed),
		TimeBudget: 60, DeltaBudget: 20, Writer: rec,
		Observe: log.Observe})
	k.RunAll(100, nil)
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	return RaceRun{Seed: seed, Trace: tr, Orders: log.Orders}
}

// TestRaceWarning checks a seed-dependent design gives the sim/race
// warning naming the two processes whose order decided the outcome,
// not a determinism failure.
func TestRaceWarning(t *testing.T) {
	m := racy()
	var seeds []uint64
	for s := range uint64(16) {
		seeds = append(seeds, s)
	}
	ds, err := CheckRaces(m, seeds, func(seed uint64) (RaceRun, error) {
		return raceRun(t, racy, seed), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 {
		t.Fatalf("diagnostics %v, want one race", ds)
	}
	d := ds[0]
	if d.Code != "sim/race" || !strings.Contains(d.Message,
		"race between tb.w and tb.r") ||
		!strings.Contains(d.Message, "seed 0 and seed") {
		t.Errorf("race: %s %s", d.Code, d.Message)
	}
}

// TestNoRaceAcrossSeeds checks the property behind the warning: the
// generated race-free designs of the reference scheduler test give
// identical traces at every seed, so CheckRaces reports nothing.
func TestNoRaceAcrossSeeds(t *testing.T) {
	r := newRNG(77)
	for i := range 300 {
		d := genDesign(r)
		mk := func() *Model { return d.model() }
		ds, err := CheckRaces(mk(), []uint64{0, 1, 2, 3},
			func(seed uint64) (RaceRun, error) {
				return raceRun(t, mk, seed), nil
			})
		if err != nil {
			t.Fatal(err)
		}
		if len(ds) != 0 {
			t.Fatalf("design %d: %v", i, ds)
		}
	}
}

// TestFindRaceSymmetric checks FindRace names the same pair whichever
// run comes first, and finds nothing between a run and itself.
func TestFindRaceSymmetric(t *testing.T) {
	m := racy()
	base := raceRun(t, racy, 0)
	if _, ok := FindRace(m, base, base); ok {
		t.Fatal("a run races with itself")
	}
	var all []RaceRun
	for s := uint64(1); s < 16; s++ {
		all = append(all, raceRun(t, racy, s))
	}
	for i, o := range all {
		s := i + 1
		r1, ok1 := FindRace(m, base, o, all...)
		r2, ok2 := FindRace(m, o, base, all...)
		if ok1 != ok2 || r1 != r2 {
			t.Errorf("seed %d: %+v vs %+v", s, r1, r2)
		}
		if ok1 && (r1.ProcA != "tb.w" || r1.ProcB != "tb.r") {
			t.Errorf("seed %d: %+v", s, r1)
		}
	}
}
