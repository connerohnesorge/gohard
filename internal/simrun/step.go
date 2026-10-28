package simrun

import (
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/coverage"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/elab"
	"github.com/connerohnesorge/gohard/v2/internal/elab/psl"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// Running is one run in progress: Start elaborates, each Step runs a
// bounded number of kernel cycles, Outcome collects the result. Run is
// the same steps in a loop, so gohard check --sim, gohard lsp sim and
// the editor's sliced runs share one code path (#35).
type Running struct {
	out  Outcome
	d    *elab.Design
	k    *sim.Kernel
	rec  *trace.Recorder
	ov   *env.Overlay
	cov  *coverage.Collector
	real bool
	done bool
}

// Start elaborates o.Testbench and prepares the kernel. When
// elaboration fails the run is already done.
func Start(o Options) *Running {
	r := &Running{out: Outcome{Testbench: strings.ToLower(o.Testbench),
		Seed: o.Seed}}
	out := &r.out
	// Every run gets an overlay of its own: a race re-run or a rebuilt
	// editor run never sees, or returns, another run's writes.
	r.ov = env.NewOverlay(o.Env.FS, o.Env.Root)
	r.real = o.Env.Real
	io := interpOptions(o, r.ov)
	code := o.Code
	if code == nil && DefaultCode != nil {
		code = elab.Interpreter(io)
	}
	// Without an interpreter the design is still elaborated, so
	// elaboration errors (and elaboration-time evaluation over
	// simulation.step_budget) are reported; the run itself is then
	// cli/not-implemented, a tool error, never a pass.
	// PSL directives run in the PSL runner, the rest in code.
	elabCode := psl.Wrap(code)
	b := eval.NewBudget(o.Sim.StepBudget)
	d, ds := elab.Elaborate(o.Result, elab.Options{Top: o.Testbench,
		Budget: b, Code: elabCode, Env: io.Env})
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
		dg := diag.New(diag.UnsupportedConstruct, "process "+
			d.Missing[0]+" (no interpreter code)")
		for _, p := range d.Model.Processes {
			if p.Path == d.Missing[0] && p.Src.Path != "" {
				dg = dg.At(p.Src.Path, p.Src.Line, p.Src.Col)
				break
			}
		}
		out.Diagnostics = append(out.Diagnostics, dg)
		return r
	}
	r.rec = trace.NewRecorder(o.Sim.DumpScope)
	r.d = d
	out.Model = d.Model
	so := sim.Options{Seed: o.Seed,
		Order: Order(o.Seed), TimeBudget: sim.Time(o.Sim.TimeBudget),
		DeltaBudget: o.Sim.DeltaBudget, Budget: b,
		StopLevel: StopLevel(o.Sim.StopLevel), Writer: r.rec,
		Uncached: o.Coverage}
	if o.Coverage {
		// Installed after elaboration: coverage counts what the run
		// executes from the kernel's initialization on.
		r.cov = coverage.New(o.Result, d)
		d.SetStmtHook(r.cov)
	} else if o.Hook != nil {
		so.Uncached = true
		d.SetStmtHook(o.Hook)
	}
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
	r.out.Asserts = r.k.Asserts()
	r.out.Files, r.out.Dirs = r.ov.Files(), r.ov.Dirs()
	r.out.Inputs, r.out.Listings = r.ov.Inputs(), r.ov.Listings()
	if t, err := r.rec.Trace(); err == nil {
		t.Header.NonReproducible = r.real
		r.out.Trace = t
	}
	if r.cov != nil {
		rep := r.cov.Report(r.out.Trace)
		r.out.Coverage, r.out.unmatched = &rep, r.cov.Unmatched()
	}
}

// Asserts are the assertion violations counted so far.
func (r *Running) Asserts() sim.Asserts {
	if r.k == nil {
		return sim.Asserts{}
	}
	return r.k.Asserts()
}

// Outcome is the finished run; call it once Done.
func (r *Running) Outcome() Outcome { return r.out }

// Design is the elaborated design; nil when elaboration failed.
func (r *Running) Design() *elab.Design { return r.d }

// Checkpoint is a frozen run in progress (#68): Resume continues it
// from where it was frozen, as often as needed, and every resumed run
// goes on exactly as the frozen one would have. The resumed runs
// share the elaborated design (its statement hook included) and the
// interpreter's file door, an env.Overlay that Resume resets to the
// files of the checkpoint: step only the run resumed last.
type Checkpoint struct {
	r     *Running
	files *env.Overlay
}

// Checkpoint freezes r, which must not be stepped again; a run that
// is done or records coverage cannot be frozen (nil).
func (r *Running) Checkpoint() *Checkpoint {
	if r.done || r.cov != nil {
		return nil
	}
	return &Checkpoint{r: r, files: r.ov.Clone()}
}

// Resume returns a run that continues from c: a sim.Kernel.Clone of
// its kernel writing to a trace.Recorder.Clone of its recorder, which
// shares the trace recorded so far instead of copying it.
func (c *Checkpoint) Resume() *Running {
	r := *c.r
	r.k = c.r.k.Clone()
	r.rec = c.r.rec.Clone()
	r.k.SetWriter(r.rec)
	r.out.Diagnostics = slices.Clone(c.r.out.Diagnostics)
	*r.ov = *c.files.Clone()
	return &r
}
