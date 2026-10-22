// Package simrun runs one testbench end to end (#35): elaborate the
// analyzed design with internal/elab, run the internal/sim kernel in
// bounded slices and record the native trace (#36). gohard check
// --sim, gohard lsp sim and the editor code lens share this one code
// path; writing files is the caller's (through env.FS).
//
// Run is a pure function of its options: no clock, no randomness (a
// non-zero seed permutes in-delta order through env.SeedOrder), no
// file writes. The design reads project files only through the
// Env.FS door, and sees the real clock and environment only under
// --real-env (Env.Real, through the doors the shell passes). Every
// file the design writes is kept in an env.Overlay of its own run and
// returned in Outcome.Files; the shell writes them to the run
// directory (Env.RunDir) once, after the run.
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

// DefaultCode registers the sequential interpreter (internal/elab):
// when Options.Code is nil and DefaultCode is set, Start runs the
// design with elab.Interpreter and the run's environment (Options.Env,
// simulation.env.*). Callers that pass DefaultCode as Options.Code get
// it with default options (no environment). With DefaultCode nil a
// run without Code is cli/not-implemented (a tool error), never a
// pass.
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
	// Code is the interpreter; nil means the registered interpreter
	// with Env (see DefaultCode).
	Code CodeFunc
	// Env is the world of the std.env natives and the design's files
	// (#18); simulation.env.epoch and .vars come from Sim.
	Env Env
	// Cancelled is polled between slices (nil: never).
	Cancelled func() bool
	// Orders, when set, records the run orders for race detection
	// (Races).
	Orders *sim.OrderLog
}

// Env is what a run sees of the world besides its options (#18). The
// zero value is a deterministic run with no project files to read.
type Env struct {
	// ToolVersion is TOOL_VERSION, the gohard release, as conditional
	// analysis sees it.
	ToolVersion string
	// Root is the absolute project root; FS reads the project below it
	// (nil: the design finds no file it did not write).
	Root string
	FS   env.FS
	// RunDir is the run directory, relative to Root and
	// slash-separated (repro.RunDir): where the shell writes
	// Outcome.Files, and where relative directory operations of the
	// design happen.
	RunDir string
	// Real is --real-env: the natives read Clock and Getenv, and the
	// trace is marked non-reproducible. Both doors must then be set,
	// and should be frozen at the start of the run (a race re-run must
	// see the same world).
	Real   bool
	Clock  env.Clock
	Getenv env.Getenv
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
	// Asserts are the assertion violations the kernel counted (#18):
	// the counts of the status JSON and the re-run code lens.
	Asserts sim.Asserts
	// Files and Dirs are what the design wrote and created, relative
	// to the project root, sorted; the shell writes them below
	// Env.RunDir.
	Files []env.Written
	Dirs  []string
	// Inputs are the project files the design read, with the hash of
	// what it read ("" for a file it did not find), sorted: the repro
	// header records them so a replay with other input data is
	// refused (repro/source-changed, #29).
	Inputs []env.Input
}

// interpOptions are the interpreter options of a run with its own
// overlay ov.
func interpOptions(o Options, ov *env.Overlay) elab.InterpOptions {
	e := elab.EnvOptions{ToolVersion: o.Env.ToolVersion,
		Epoch: o.Sim.Env.Epoch, Vars: map[string]string{},
		Root: o.Env.Root, RunDir: o.Env.RunDir, Real: o.Env.Real,
		FS: ov}
	if o.Result != nil {
		e.Standard = o.Result.Standard
	}
	for _, v := range o.Sim.Env.Vars {
		e.Vars[v.Name] = v.Value
	}
	if o.Env.Real {
		e.Clock, e.Getenv = o.Env.Clock, o.Env.Getenv
	}
	return elab.InterpOptions{FS: ov, Env: e}
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
