package elab_test

import (
	"reflect"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/corpus"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/gen/vhdlgen"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/simrun"
)

// elabBudgets are the step budgets FuzzElab elaborates under, chosen
// by the seed: small enough that elaboration runs out at many points,
// the last large enough for every corpus design (P7).
var elabBudgets = []int64{1, 300, 30_000, 3_000_000}

// elabTops is the most testbench candidates FuzzElab elaborates per
// input.
const elabTops = 3

// FuzzElab elaborates designs through simrun.Start, the code path of
// gohard check --sim and the editor, under a small
// simulation.step_budget (elabBudgets[seed>>2%4]) (#58). With text
// false the design is generated (internal/gen/vhdlgen) from (seed,
// data); with text true data is one VHDL file analyzed under
// vhdlgen.Standards[seed%4] (the seeds are the corpus designs, each
// as one file). As in gohard check --sim a design with analysis errors
// is not elaborated, and the first elabTops testbench candidates are.
// Properties: no panic; no hang (every evaluation counts against the
// step budget); every failure is a diagnostic (P6): a run that ends at
// elaboration has an error, and its status is Exceeded exactly when one
// of them is sim/budget-exceeded; every diagnostic has a known code and
// a position in an input file or none; elaborating again gives the
// same diagnostics and model size (P1); and a generated design
// analyzes without errors and fails only by running out of steps.
// Saved inputs live in testdata/fuzz/FuzzElab (local corpus).
func FuzzElab(f *testing.F) {
	man, err := corpusRead(corpus.ManifestPath)
	if err != nil {
		f.Fatal(err)
	}
	m, err := corpus.ParseManifest(man)
	if err != nil {
		f.Fatal(err)
	}
	for _, d := range m.Designs {
		r := d.Runs()[0]
		src, err := r.Sources(corpusRead)
		if err != nil {
			f.Fatal(err)
		}
		var text []byte
		for _, p := range r.Paths {
			text = append(text, src[p]...)
		}
		std := 0
		for i, s := range vhdlgen.Standards {
			if s == r.Standard {
				std = i
			}
		}
		// The largest budget, so every seed elaborates in full.
		f.Add(uint64(std+3<<2), true, text)
	}
	for seed := range uint64(16) {
		f.Add(seed, false, []byte(nil))
	}
	f.Fuzz(checkElab)
}

func checkElab(t *testing.T, seed uint64, text bool, data []byte) {
	var files []sema.Source
	std := vhdlgen.Standards[seed%4]
	var gen *vhdlgen.Design
	if text {
		files = []sema.Source{{Path: "fuzz.vhd", Text: data}}
	} else {
		d := vhdlgen.Generate(seed, data, vhdlgen.Options{
			Racy: seed&1 == 1})
		gen, std = &d, d.Standard
		for _, f := range d.Files {
			files = append(files, sema.Source{Path: f.Path, Text: f.Text})
		}
	}
	fail := func(format string, args ...any) {
		t.Helper()
		if gen != nil {
			args = append(args, gen.Text())
		} else {
			args = append(args, data)
		}
		t.Fatalf("seed %d text %t: "+format+"\n%s", append(
			[]any{seed, text}, args...)...)
	}
	cfg := config.Defaults()
	cfg.Standard = std
	cfg.Simulation.StepBudget = elabBudgets[seed>>2%4]
	res := sema.Analyze(sema.Input{Config: cfg, Files: files})
	for _, d := range res.Diagnostics {
		if d.Severity != diag.Error {
			continue
		}
		if gen != nil {
			fail("generated design does not analyze: %s:%d:%d: %s",
				d.Path, d.Line, d.Col, d.Message)
		}
		return
	}
	cands := simrun.Candidates(res, cfg.Simulation)
	if gen != nil && (len(cands) == 0 || cands[0] != gen.Top) {
		fail("candidates %v, want %s first", cands, gen.Top)
	}
	for i, tb := range cands {
		if i == elabTops {
			break
		}
		o := simrun.Options{Result: res, Sim: cfg.Simulation, Testbench: tb,
			Seed: seed}
		a := elaborate(o)
		checkElabOutcome(a, files, fail)
		if gen != nil {
			for _, d := range a.out.Diagnostics {
				if d.Severity == diag.Error &&
					d.Code != diag.SimBudgetExceeded {
					fail("%s: error %s: %s", tb, d.Code, d.Message)
				}
			}
		}
		b := elaborate(o)
		if !reflect.DeepEqual(a.out.Diagnostics, b.out.Diagnostics) ||
			a.out.Status != b.out.Status || a.done != b.done ||
			a.procs != b.procs || a.signals != b.signals {
			fail("%s: two elaborations differ:\n%v %s %d %d\n%v %s %d %d",
				tb, a.out.Diagnostics, a.out.Status, a.procs, a.signals,
				b.out.Diagnostics, b.out.Status, b.procs, b.signals)
		}
	}
}

// elaborated is what FuzzElab compares of one elaboration.
type elaborated struct {
	out            simrun.Outcome
	done           bool
	procs, signals int
}

// elaborate starts a run of o and cancels it before the first cycle.
func elaborate(o simrun.Options) elaborated {
	r := simrun.Start(o)
	e := elaborated{done: r.Done()}
	if !e.done {
		r.Cancel()
	}
	e.out = r.Outcome()
	if m := e.out.Model; m != nil {
		e.procs, e.signals = len(m.Processes), len(m.Signals)
	}
	return e
}

// checkElabOutcome checks that a failed elaboration is a diagnostic
// and that every diagnostic is well formed.
func checkElabOutcome(e elaborated, files []sema.Source,
	fail func(string, ...any),
) {
	out := e.out
	errs, budget := 0, false
	for _, d := range out.Diagnostics {
		if _, ok := d.Code.Info(); !ok || d.Message == "" {
			fail("diagnostic with code %q, message %q", d.Code, d.Message)
		}
		if d.Path != "" && !inFiles(d.Path, files) || d.Path != "" &&
			d.Line < 1 {
			fail("diagnostic at %s:%d: %s", d.Path, d.Line, d.Message)
		}
		if d.Severity == diag.Error {
			errs++
			budget = budget || d.Code == diag.SimBudgetExceeded
		}
	}
	if !e.done {
		if errs > 0 || out.Status != sim.Cancelled {
			fail("elaborated with %d errors, status %s: %v", errs,
				out.Status, out.Diagnostics)
		}
		return
	}
	if errs == 0 {
		fail("elaboration ended (status %s) without an error: %v",
			out.Status, out.Diagnostics)
	}
	if (out.Status == sim.Exceeded) != budget {
		fail("status %s with diagnostics %v", out.Status, out.Diagnostics)
	}
}

func inFiles(p string, files []sema.Source) bool {
	for _, f := range files {
		if f.Path == p {
			return true
		}
	}
	return false
}
