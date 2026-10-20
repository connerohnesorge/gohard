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
	"github.com/connerohnesorge/gohard/internal/elab/psl"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
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
// share one step budget (simulation.step_budget).
func Run(o Options) Outcome {
	out := Outcome{Testbench: strings.ToLower(o.Testbench), Seed: o.Seed}
	code := o.Code
	if code == nil {
		code = DefaultCode
	}
	// Without an interpreter the design is still elaborated, so
	// elaboration errors (and elaboration-time evaluation over
	// simulation.step_budget) are reported; the run itself is then
	// cli/not-implemented, a tool error, never a pass.
	// PSL directives run in the PSL runner, the rest in code.
	elabCode := psl.Wrap(code)
	b := eval.NewBudget(o.Sim.StepBudget)
	d, ds := elab.Elaborate(o.Result, elab.Options{Top: o.Testbench,
		Budget: b, Code: elabCode})
	out.Diagnostics = append(out.Diagnostics, ds...)
	if d == nil || len(ds) > 0 && failed(ds) {
		out.Status = sim.Failed
		if exceeded(ds) {
			out.Status = sim.Exceeded
		}
		return out
	}
	if code == nil {
		out.Status = sim.Failed
		out.Diagnostics = append(out.Diagnostics, diag.New(
			diag.CLINotImplemented, "check --sim"))
		return out
	}
	if len(d.Missing) > 0 {
		out.Status = sim.Failed
		out.Diagnostics = append(out.Diagnostics, diag.New(
			diag.UnsupportedConstruct, "process "+d.Missing[0]+
				" (no interpreter code)"))
		return out
	}
	rec := trace.NewRecorder(o.Sim.DumpScope)
	out.Model = d.Model
	so := sim.Options{Seed: o.Seed,
		Order: Order(o.Seed), TimeBudget: sim.Time(o.Sim.TimeBudget),
		DeltaBudget: o.Sim.DeltaBudget, Budget: b,
		StopLevel: StopLevel(o.Sim.StopLevel), Writer: rec}
	if o.Orders != nil {
		so.Observe = o.Orders.Observe
	}
	k, err := sim.New(d.Model, so)
	if err != nil {
		out.Status = sim.Failed
		out.Diagnostics = append(out.Diagnostics, diag.New(
			diag.SimRuntimeError, err.Error(), d.Model.Testbench,
			"0 fs"))
		return out
	}
	out.Status = k.RunAll(Slice, o.Cancelled)
	out.Diagnostics = append(out.Diagnostics, k.Diagnostics()...)
	if t, err := rec.Trace(); err == nil {
		out.Trace = t
	}
	return out
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
