// Package simrun runs one testbench end to end (#35): elaborate the
// analyzed design with internal/elab, run the internal/sim kernel in
// bounded slices and record the native trace (#36). gohard check
// --sim, gohard lsp sim and the editor code lens share this one code
// path; writing files is the caller's (through env.FS).
//
// Run is a pure function of its options: no clock, no randomness (a
// non-zero seed permutes in-delta order through env.SeedOrder), no
// file access.
package simrun

import (
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/elab"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// CodeFunc builds a process's behaviour: the sequential interpreter.
type CodeFunc = func(*elab.Design, *elab.Proc) (sim.Code, error)

// DefaultCode is the interpreter Run uses when Options.Code is nil:
// the sequential interpreter (internal/elab). A run with a nil one is
// cli/not-implemented (a tool error), never a pass.
var DefaultCode CodeFunc = elab.Interpret

// Slice is the number of kernel cycles between cancellation checks.
const Slice = 256

// SliceAt is the number of kernel cycles of the editor's slice i
// (1-based): 1, 2, 4, ... up to Slice. The first simulated-time report
// then follows the first cycle (the initialization of a large design
// takes most of a slice's time); it depends on i only, so a rebuilt
// run reaches every position in the same cycles.
func SliceAt(i int) int {
	if i < 1 {
		i = 1
	}
	if i > 9 {
		return Slice
	}
	return min(Slice, 1<<(i-1))
}

// Options configure Run.
type Options struct {
	Result *sema.Result
	// Sim are the simulation.* keys in effect.
	Sim config.Simulation
	// Testbench is the entity or configuration to run.
	Testbench string
	Seed      uint64
	// Code is the interpreter; nil means DefaultCode.
	Code CodeFunc
	// Cancelled is polled between slices (nil: never).
	Cancelled func() bool
	// Orders, when set, records the run orders for race detection
	// (Races).
	Orders *sim.OrderLog
}

// Outcome is one finished run.
type Outcome struct {
	Testbench string
	Seed      uint64
	Status    sim.Status
	// Diagnostics are the elaboration and simulation diagnostics in
	// emission order.
	Diagnostics []diag.Diagnostic
	// Trace is the recorded trace; nil when the run never started.
	Trace *trace.Trace
	// Model is the elaborated model; nil when elaboration failed.
	Model *sim.Model
}

// Candidates returns the testbench candidates of #35: every portless
// project entity, then the simulation.testbenches entries not already
// listed, with duplicates removed (names lower case).
func Candidates(r *sema.Result, s config.Simulation) []string {
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		n = strings.ToLower(n)
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, u := range r.Units {
		if u.Kind != sema.UnitEntity || u.Decl == nil || u.Duplicate ||
			u.Opaque || !portless(u) {
			continue
		}
		add(string(u.Name))
	}
	for _, t := range s.Testbenches {
		add(t)
	}
	return out
}

func portless(u *sema.Unit) bool {
	if u.Region == nil {
		return true
	}
	return !slices.ContainsFunc(u.Region.Decls, func(d *sema.Decl) bool {
		return d.Kind == sema.DeclPort
	})
}

// StopLevel maps simulation.stop_level to a trace severity.
func StopLevel(s string) trace.Severity {
	switch s {
	case "note":
		return trace.Note
	case "warning":
		return trace.Warning
	case "failure":
		return trace.Failure
	}
	return trace.Error
}

// Order is the in-delta process order for seed: declaration order for
// seed 0, else env.SeedOrder.
func Order(seed uint64) env.Order {
	if seed == 0 {
		return nil
	}
	return env.SeedOrder{Seed: seed}
}

// Run elaborates and simulates o.Testbench. Elaboration and simulation
// share one step budget (simulation.step_budget). It is Start, Step
// until done (polling o.Cancelled between slices) and Outcome: the
// code path the editor runs one slice at a time.
func Run(o Options) Outcome {
	r := Start(o)
	for !r.Done() {
		if o.Cancelled != nil && o.Cancelled() {
			r.Cancel()
			break
		}
		r.Step(Slice)
	}
	return r.Outcome()
}

func failed(ds []diag.Diagnostic) bool {
	return slices.ContainsFunc(ds, func(d diag.Diagnostic) bool {
		return d.Severity == diag.Error
	})
}

func exceeded(ds []diag.Diagnostic) bool {
	return slices.ContainsFunc(ds, func(d diag.Diagnostic) bool {
		return d.Code == diag.SimBudgetExceeded
	})
}

// Budgets returns the repro budgets of s.
func Budgets(s config.Simulation) (time string, delta, step int64) {
	return s.TimeBudget.String(), s.DeltaBudget, s.StepBudget
}

// SeedText is how a seed is shown ("seed N").
func SeedText(seed uint64) string {
	return "seed " + strconv.FormatUint(seed, 10)
}
