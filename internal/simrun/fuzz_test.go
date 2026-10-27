package simrun

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/gen/vhdlgen"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// fuzzSim are the small budgets FuzzSim runs under.
var fuzzSim = func() config.Simulation {
	s := config.Defaults().Simulation
	s.TimeBudget = 2 * config.Microsecond
	s.DeltaBudget = 1000
	s.StepBudget = 500_000
	return s
}()

// maxCycles bounds the kernel cycles of one FuzzSim run: far more than
// the budgets let a generated design use, so a run still going is a
// hang (P7).
const maxCycles = 1 << 22

// FuzzSim runs small generated testbenches (internal/gen/vhdlgen)
// under small time, delta and step budgets (#58). The design comes
// from (seed, data), racy lets processes share a variable,
// and the run's seed is seed. Properties: the design analyzes without
// an error; the run never panics and ends within maxCycles kernel
// cycles; its only errors are sim/budget-exceeded; a second run at the
// same seed gives the same trace bytes, status and diagnostics (P1); a
// race-free design gives the same trace at seeds 0 to 3 (unless a step
// budget ran out, which stops mid-delta); and for a racy design Races
// reports sim/race exactly when the traces at seed 0 and another seed
// differ. Saved inputs live in testdata/fuzz/FuzzSim (local corpus).
func FuzzSim(f *testing.F) {
	for seed := range uint64(16) {
		f.Add(seed, seed%4 == 3, []byte(nil))
	}
	f.Add(uint64(1<<63+5), false, []byte("\x00\x01\x02\x03\x04\x05"))
	f.Fuzz(checkSim)
}

func checkSim(t *testing.T, seed uint64, racy bool, data []byte) {
	if DefaultCode == nil {
		t.Skip("no interpreter registered")
	}
	d := vhdlgen.Generate(seed, data, vhdlgen.Options{Racy: racy})
	res := analyzeGenerated(d)
	for _, dg := range res.Diagnostics {
		if dg.Severity == diag.Error {
			t.Fatalf("seed %d: generated design does not analyze: "+
				"%s:%d:%d: %s\n%s", seed, dg.Path, dg.Line, dg.Col,
				dg.Message, d.Text())
		}
	}
	o := Options{Result: res, Sim: fuzzSim, Testbench: d.Top, Seed: seed}
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf("seed %d racy %t data %q: "+format+"\n%s",
			append(append([]any{seed, racy, data}, args...), d.Text())...)
	}
	a := runBounded(o, fail)
	for _, dg := range a.Diagnostics {
		if dg.Severity == diag.Error && dg.Code != diag.SimBudgetExceeded {
			fail("error %s: %s", dg.Code, dg.Message)
		}
	}
	if a.Trace == nil {
		fail("no trace (status %s)", a.Status)
	}
	b := runBounded(o, fail)
	sameRun(a, b, fail, "two runs at seed %d", seed)
	if stepExceeded(a) {
		return
	}
	if d.RaceFree {
		for s := range uint64(4) {
			if s == seed {
				continue
			}
			o.Seed = s
			sameRun(a, runBounded(o, fail), fail, "seeds %d and %d", seed,
				s)
		}
		return
	}
	other := seed | 1
	o.Seed = 0
	x := runBounded(o, fail)
	o.Seed = other
	y := runBounded(o, fail)
	if x.Trace == nil || y.Trace == nil || stepExceeded(x) ||
		stepExceeded(y) {
		return
	}
	differ := !bytes.Equal(trace.Encode(x.Trace), trace.Encode(y.Trace))
	ds := Races(o, []uint64{0, other})
	reported := len(ds) > 0 && ds[0].Code == diag.SimRace
	if differ != reported {
		fail("traces at seeds 0 and %d differ: %t, Races: %v", other,
			differ, ds)
	}
}

// analyzeGenerated analyzes a generated design under its standard.
func analyzeGenerated(d vhdlgen.Design) *sema.Result {
	cfg := config.Defaults()
	cfg.Standard = d.Standard
	var src []sema.Source
	for _, f := range d.Files {
		src = append(src, sema.Source{Path: f.Path, Text: f.Text})
	}
	return sema.Analyze(sema.Input{Config: cfg, Files: src})
}

// runBounded is Run with the maxCycles bound.
func runBounded(o Options, fail func(string, ...any)) Outcome {
	r := Start(o)
	for cycles := 0; !r.Done(); cycles += Slice {
		if cycles >= maxCycles {
			fail("still running after %d kernel cycles at %d fs", cycles,
				r.Now())
		}
		r.Step(Slice)
	}
	return r.Outcome()
}

// stepExceeded reports whether the step budget ran out: that stops a
// run in the middle of a delta, where the processes run so far depend
// on the seed.
func stepExceeded(o Outcome) bool {
	for _, d := range o.Diagnostics {
		if d.Code == diag.SimBudgetExceeded &&
			strings.HasPrefix(d.Message, "step ") {
			return true
		}
	}
	return false
}

// sameRun fails unless a and b have the same status, diagnostics and
// trace bytes.
func sameRun(a, b Outcome, fail func(string, ...any), what string,
	args ...any,
) {
	ta, tb := encode(a.Trace), encode(b.Trace)
	switch {
	case a.Status != b.Status:
		fail(what+": status %s and %s", append(args, a.Status,
			b.Status)...)
	case !reflect.DeepEqual(a.Diagnostics, b.Diagnostics):
		fail(what+": diagnostics\n%v\n%v", append(args, a.Diagnostics,
			b.Diagnostics)...)
	case !bytes.Equal(ta, tb):
		fail(what+": traces differ\n%s\n%s", append(args, ta, tb)...)
	}
}

func encode(t *trace.Trace) []byte {
	if t == nil {
		return nil
	}
	return trace.Encode(t)
}

// TestFuzzSimReach: the FuzzSim properties are not vacuous. Of 64
// generated designs some end quiet and some by std.env.finish, and of
// 64 racy ones some do race (Races reports sim/race).
func TestFuzzSimReach(t *testing.T) {
	if DefaultCode == nil {
		t.Skip("no interpreter registered")
	}
	quiet, stopped, races := 0, 0, 0
	for seed := range uint64(64) {
		d := vhdlgen.Generate(seed, nil, vhdlgen.Options{})
		o := Options{Result: analyzeGenerated(d), Sim: fuzzSim,
			Testbench: d.Top, Seed: seed}
		switch runBounded(o, t.Fatalf).Status {
		case sim.Quiet:
			quiet++
		case sim.Stopped:
			stopped++
		}
		d = vhdlgen.Generate(seed, nil, vhdlgen.Options{Racy: true})
		if d.RaceFree {
			continue
		}
		o.Result = analyzeGenerated(d)
		ds := Races(o, []uint64{0, seed | 1})
		if len(ds) > 0 && ds[0].Code == diag.SimRace {
			races++
		}
	}
	if quiet == 0 || stopped == 0 || races == 0 {
		t.Errorf("of 64 designs %d end quiet, %d stopped; %d of 64 "+
			"racy designs race", quiet, stopped, races)
	}
	t.Logf("quiet %d, stopped %d, racing %d", quiet, stopped, races)
}
