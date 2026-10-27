package corpus

import (
	"fmt"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/elab"
	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/elab/psl"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// CodeFunc builds the behaviour of one elaborated process: the
// sequential interpreter's entry point (elab.Options.Code).
type CodeFunc = func(*elab.Design, *elab.Proc) (sim.Code, error)

// Options configure Execute.
type Options struct {
	Seed uint64
	// Code is the sequential interpreter. Nil, or a design with a
	// process it gives no code, stops after elaboration with
	// Result.Pending set: the run cannot claim a simulation outcome.
	Code CodeFunc
	// Slice is the number of kernel cycles per RunAll slice (0: 1024).
	Slice int
	// Cancelled is polled between slices (nil: never).
	Cancelled func() bool
	// Profile, when set, records the steps of the run's interpreted
	// calls by package (the #35 native-body rule).
	Profile *sema.Profile
	// Simulating, when set, is called once analysis and elaboration
	// are done, right before the kernel is built and run: the
	// benchmarks time the simulation alone from there (#59).
	Simulating func()
}

// Produced is one produced diagnostic with the simulated time of the
// event it reports (Timed false when it has none).
type Produced struct {
	diag.Diagnostic
	Time  trace.Time
	Timed bool
}

// Result is the outcome of one run.
type Result struct {
	// Messages are the diagnostics in #20 output order.
	Messages []Produced
	Exit     int
	Status   diag.Status
	// Simulated reports that the kernel ran; StopTime is then the
	// simulated time the run ended at.
	Simulated bool
	StopTime  trace.Time
	// Trace is the recorded trace and Text its canonical form (nil
	// without a simulation).
	Trace *trace.Trace
	Text  []byte
	// Hier is the elaboration hierarchy of the testbench (nil when
	// analysis failed).
	Hier *hier.Result
	// Pending lists the processes without code when the run stopped
	// before simulating for want of an interpreter.
	Pending []string
	// Design is the elaborated design (nil when not elaborated).
	Design *elab.Design
	// Steps are the evaluation steps the run used, elaboration
	// included (simulation.step_budget counts them).
	Steps int64
}

// Config returns the run's project configuration: the design's
// gohard.yaml (or the #28 defaults) under the run's standard.
func (r Run) Config(src map[string][]byte) (config.Config, error) {
	cfg := config.Defaults()
	if b, ok := src[configKey]; ok {
		c, ds := config.Parse(config.FileName, b)
		for _, d := range ds {
			if d.Severity == diag.Error {
				return cfg, fmt.Errorf("%s: %s:%d: %s", r.Name(),
					r.Design.Config, d.Line, d.Message)
			}
		}
		cfg = c
	}
	cfg.Standard = r.Standard
	return cfg, nil
}

// Analyze runs analysis on the sources of r.
func (r Run) Analyze(src map[string][]byte) (*sema.Result, config.Config,
	error,
) {
	cfg, err := r.Config(src)
	if err != nil {
		return nil, cfg, err
	}
	in := sema.Input{Config: cfg, Jobs: env.Sequential{}}
	for _, p := range r.Paths {
		in.Files = append(in.Files, sema.Source{Path: p, Text: src[p]})
	}
	return sema.Analyze(in), cfg, nil
}

// Execute runs r the way `gohard check --sim --tb <testbench>` does
// (#20, #35): analysis and the per-instance static checks; when they
// found no error, elaboration of the testbench and the simulation at
// the seed, under the configuration's budgets and stop level.
func Execute(r Run, src map[string][]byte, o Options) (*Result, error) {
	res, cfg, err := r.Analyze(src)
	if err != nil {
		return nil, err
	}
	out := &Result{}
	var ds []Produced
	add := func(xs []diag.Diagnostic) {
		for _, d := range xs {
			ds = append(ds, Produced{Diagnostic: d})
		}
	}
	add(res.Diagnostics)
	h := hier.Build(res, hier.Options{})
	add(h.Diagnostics)
	out.Hier = hier.Build(res, hier.Options{Tops: []string{
		r.Design.Testbench}})
	defer func() { out.finish(ds) }()
	if blocking(ds) {
		return out, nil
	}
	sc := cfg.Simulation
	b := eval.NewBudget(sc.StepBudget)
	if o.Profile != nil {
		o.Profile.Attach(b)
	}
	// PSL directives run in the PSL runner, the rest in o.Code.
	d, eds := elab.Elaborate(res, elab.Options{Top: r.Design.Testbench,
		Budget: b, Code: psl.Wrap(o.Code)})
	add(eds)
	out.Design = d
	if d == nil || blocking(ds) {
		return out, nil
	}
	if o.Code == nil || len(d.Missing) > 0 {
		out.Pending = slices.Clone(d.Missing)
		if o.Code == nil {
			out.Pending = []string{"(no interpreter)"}
		}
		return out, nil
	}
	if o.Simulating != nil {
		o.Simulating()
	}
	rec := trace.NewRecorder(sc.DumpScope)
	opts := sim.Options{Seed: o.Seed, TimeBudget: sim.Time(sc.TimeBudget),
		DeltaBudget: sc.DeltaBudget, Budget: b, Writer: rec,
		StopLevel: stopLevel(sc.StopLevel)}
	if o.Seed != 0 {
		opts.Order = env.SeedOrder{Seed: o.Seed}
	}
	k, err := sim.New(d.Model, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.Name(), err)
	}
	slice := o.Slice
	if slice <= 0 {
		slice = 1024
	}
	k.RunAll(slice, o.Cancelled)
	if err := k.Err(); err != nil {
		return nil, fmt.Errorf("%s: kernel: %w", r.Name(), err)
	}
	out.Simulated, out.StopTime = true, k.Now()
	out.Steps = b.Used()
	t, err := rec.Trace()
	if err != nil {
		return nil, fmt.Errorf("%s: trace: %w", r.Name(), err)
	}
	if err := answerKey(t); err != nil {
		return nil, fmt.Errorf("%s: %w", r.Name(), err)
	}
	out.Trace, out.Text = t, trace.Encode(t)
	ds = append(ds, timed(k.Diagnostics(), t)...)
	return out, nil
}

// blocking reports whether a diagnostic stops the run before the
// simulation: any error or tool-class diagnostic.
func blocking(ds []Produced) bool {
	for _, d := range ds {
		if d.Severity == diag.Error || d.Class() == diag.Tool {
			return true
		}
	}
	return false
}

func (out *Result) finish(ds []Produced) {
	slices.SortStableFunc(ds, func(a, b Produced) int {
		return diag.Compare(a.Diagnostic, b.Diagnostic)
	})
	ds = slices.CompactFunc(ds, func(a, b Produced) bool {
		return diag.Compare(a.Diagnostic, b.Diagnostic) == 0 &&
			a.Time == b.Time && a.Timed == b.Timed
	})
	out.Messages = ds
	plain := make([]diag.Diagnostic, len(ds))
	for i, d := range ds {
		plain[i] = d.Diagnostic
	}
	out.Exit = diag.ExitCode(plain, false, diag.NoSignal)
	out.Status = diag.StatusOf(out.Exit)
}

// timed attaches to every assertion and report diagnostic the time of
// the first unused trace event whose message it carries (the trace
// sorts the events of one delta by process, the kernel emits them in
// run order), to a PSL cover-hit diagnostic the time of the cover's
// first hit (the first cover-hit event of its source line), and to
// the others the end time of the run.
func timed(ds []diag.Diagnostic, t *trace.Trace) []Produced {
	var evs []trace.Event
	for _, e := range t.Events {
		if e.Kind == trace.Assert || e.Kind == trace.Report {
			evs = append(evs, e)
		}
	}
	firstHit := func(d diag.Diagnostic) (trace.Time, bool) {
		for _, e := range t.Events {
			if e.Kind == trace.CoverHit && e.File == d.Path &&
				e.Line == d.Line {
				return e.Time, true
			}
		}
		return 0, false
	}
	out := make([]Produced, 0, len(ds))
	for _, d := range ds {
		p := Produced{Diagnostic: d, Time: t.Header.End, Timed: true}
		switch d.Code {
		case diag.SimCoverHit:
			if at, ok := firstHit(d); ok {
				p.Time = at
			}
		case diag.SimAssertError, diag.SimAssertNote,
			diag.SimAssertWarning:
			i := slices.IndexFunc(evs, func(e trace.Event) bool {
				return strings.HasSuffix(d.Message, e.Message)
			})
			if i >= 0 {
				p.Time = evs[i].Time
				evs = slices.Delete(evs, i, i+1)
			}
		}
		out = append(out, p)
	}
	return out
}

func stopLevel(s string) trace.Severity {
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

// Hierarchy analyzes r and returns the elaboration hierarchy of its
// testbench (the elaboration oracle's subject, #8).
func Hierarchy(r Run, read ReadFunc) (*hier.Result, error) {
	src, err := r.Sources(read)
	if err != nil {
		return nil, err
	}
	res, _, err := r.Analyze(src)
	if err != nil {
		return nil, err
	}
	return hier.Build(res, hier.Options{Tops: []string{
		r.Design.Testbench}}), nil
}
