package simrun

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/elab"
	"github.com/connerohnesorge/gohard/internal/elab/psl"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// Running is one run in progress: Start elaborates, each Step runs a
// bounded number of kernel cycles, Outcome collects the result. Run is
// the same steps in a loop, so gohard check --sim, gohard lsp sim and
// the editor's sliced runs share one code path (#35).
type Running struct {
	out  Outcome
	k    *sim.Kernel
	rec  *trace.Recorder
	done bool
}

// Start elaborates o.Testbench and prepares the kernel. When
// elaboration fails the run is already done.
func Start(o Options) *Running {
	r := &Running{out: Outcome{Testbench: strings.ToLower(o.Testbench),
		Seed: o.Seed}}
	out := &r.out
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
	r.done = true
	if d == nil || len(ds) > 0 && failed(ds) {
		out.Status = sim.Failed
		if exceeded(ds) {
			out.Status = sim.Exceeded
		}
		return r
	}
	if code == nil {
		out.Status = sim.Failed
		out.Diagnostics = append(out.Diagnostics, diag.New(
			diag.CLINotImplemented, "check --sim"))
		return r
	}
	if len(d.Missing) > 0 {
		out.Status = sim.Failed
		out.Diagnostics = append(out.Diagnostics, diag.New(
			diag.UnsupportedConstruct, "process "+d.Missing[0]+
				" (no interpreter code)"))
		return r
	}
	r.rec = trace.NewRecorder(o.Sim.DumpScope)
	out.Model = d.Model
	so := sim.Options{Seed: o.Seed,
		Order: Order(o.Seed), TimeBudget: sim.Time(o.Sim.TimeBudget),
		DeltaBudget: o.Sim.DeltaBudget, Budget: b,
		StopLevel: StopLevel(o.Sim.StopLevel), Writer: r.rec}
	if o.Orders != nil {
		so.Observe = o.Orders.Observe
	}
	k, err := sim.New(d.Model, so)
	if err != nil {
		out.Status = sim.Failed
		out.Diagnostics = append(out.Diagnostics, diag.New(
			diag.SimRuntimeError, err.Error(), d.Model.Testbench,
			"0 fs"))
		return r
	}
	r.k, r.done = k, false
	return r
}

// Done reports whether the run ended.
func (r *Running) Done() bool { return r.done }

// Now is the simulated time reached (0 before the kernel runs).
func (r *Running) Now() sim.Time {
	if r.k == nil {
		return 0
	}
	return r.k.Now()
}

// Step runs at most cycles kernel cycles and reports whether the run
// ended.
func (r *Running) Step(cycles int) bool {
	if r.done {
		return true
	}
	if st := r.k.Run(cycles); st.Done() {
		r.end()
	}
	return r.done
}

// Cancel stops the run between slices; the trace ends at the current
// time.
func (r *Running) Cancel() {
	if r.done {
		return
	}
	r.k.Cancel()
	r.end()
}

func (r *Running) end() {
	r.done = true
	r.out.Status = r.k.Status()
	r.out.Diagnostics = append(r.out.Diagnostics, r.k.Diagnostics()...)
	if t, err := r.rec.Trace(); err == nil {
		r.out.Trace = t
	}
}

// Outcome is the finished run; call it once Done.
func (r *Running) Outcome() Outcome { return r.out }
