package psl

import (
	"errors"
	"slices"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// maxStates bounds the pending obligations (assert) or running
// sequence states (cover) of one directive: past it the directive is a
// run-time error, not an unbounded memory use (P7).
const maxStates = 1 << 14

// runner is the process of one directive. Its state is the set of open
// obligations (assert) or running matches (cover), the running matches
// of an assert's antecedent (as) and whether the directive is covered
// (#18, PslIsCovered and PslIsAssertCovered): immutable values, so
// Clone copies the slices only. epoch is the kernel's ClearPslState
// epoch the state belongs to (sync).
type runner struct {
	dir     *directive
	started bool
	ticked  bool
	obls    []*prop
	ss      []*sere
	as      []*sere
	hits    int
	covered bool
	epoch   uint64
}

// Clone implements sim.Code.
func (r *runner) Clone() sim.Code {
	c := *r
	c.obls = slices.Clone(r.obls)
	c.ss = slices.Clone(r.ss)
	c.as = slices.Clone(r.as)
	return &c
}

// sync drops the directive's state when the design called
// ClearPslState since the runner last ran: the directive starts over
// at its next tick, with no hits, no open obligation and not covered.
func (r *runner) sync(c *sim.Ctx) {
	if e := c.PSLEpoch(); e != r.epoch {
		*r = runner{dir: r.dir, started: r.started, epoch: e}
	}
}

// cover records the directive as covered, once per epoch.
func (r *runner) cover(c *sim.Ctx) {
	if !r.covered {
		r.covered = true
		r.as = nil
		c.PSLCover(r.dir.kind == dAssert)
	}
}

// Resume implements sim.Code: wait on the clock, and at every tick
// sample the booleans and advance the directive.
func (r *runner) Resume(c *sim.Ctx) (sim.Wait, error) {
	d := r.dir
	if !r.started {
		r.started = true
		switch {
		case d.formal != "":
			c.Diagnostic(d.src, diag.New(diag.SimNeedsFormal, d.formal))
			return sim.Wait{}, nil
		case d.kind == dIgnore:
			return sim.Wait{}, nil
		case d.unsup != "":
			c.Diagnostic(d.src, diag.New(diag.UnsupportedConstruct,
				d.unsup))
			return sim.Wait{}, nil
		}
		return sim.Wait{On: d.clock.on}, nil
	}
	r.sync(c)
	ev := r.bound(c)
	tick, err := r.tick(c, ev)
	if err != nil {
		return sim.Wait{}, err
	}
	if tick {
		if err := r.step(c, ev); err != nil {
			return sim.Wait{}, err
		}
	}
	return sim.Wait{On: d.clock.on}, nil
}

// bound is the directive's evaluator with every signal it reads bound
// to its current value.
func (r *runner) bound(c *sim.Ctx) *sema.Evaluator {
	ev := r.dir.ev
	for _, s := range r.dir.sigs {
		ev = ev.Bind(s.decl, c.Value(s.id))
	}
	return ev
}

func (r *runner) value(c *sim.Ctx, ev *sema.Evaluator, e *sema.Expr) (
	bool, error,
) {
	v, err := ev.Value(e)
	if b := c.Budget(); b != nil {
		if berr := b.Step(ev.Used()); berr != nil {
			return false, berr
		}
	}
	if err != nil {
		return false, err
	}
	t, ok := truth(v)
	if !ok {
		return false, errors.New("PSL boolean is not a boolean, bit " +
			"or std_ulogic value")
	}
	return t, nil
}

// truth reads a BOOLEAN, BIT or STD_ULOGIC value ('1' and 'H' hold).
func truth(v eval.Value) (bool, bool) {
	if v.T == nil || v.T.Kind != eval.KindEnum || v.I < 0 ||
		int(v.I) >= len(v.T.Literals) {
		return false, false
	}
	switch v.T.Literals[v.I] {
	case "true", "'1'", "'H'", "'h'":
		return true, true
	}
	return false, true
}

// x01 is To_X01 of a clock value: 1, 0, or -1 for anything else.
func x01(v eval.Value) int {
	if v.T == nil || v.T.Kind != eval.KindEnum || v.I < 0 ||
		int(v.I) >= len(v.T.Literals) {
		return -1
	}
	switch v.T.Literals[v.I] {
	case "true", "'1'", "'H'", "'h'":
		return 1
	case "false", "'0'", "'L'", "'l'":
		return 0
	}
	return -1
}

// tick reports whether the clock ticks in the current cycle.
func (r *runner) tick(c *sim.Ctx, ev *sema.Evaluator) (bool, error) {
	k := r.dir.clock
	switch k.edge {
	case rise, fall:
		if !c.Event(k.sig) {
			return false, nil
		}
		now, was := x01(c.Value(k.sig)), x01(c.LastValue(k.sig))
		if k.edge == rise {
			return now == 1 && was == 0, nil
		}
		return now == 0 && was == 1, nil
	case event:
		if !c.Event(k.sig) {
			return false, nil
		}
		return r.value(c, ev, k.cond)
	}
	if !slices.ContainsFunc(k.on, c.Event) {
		return false, nil
	}
	return r.value(c, ev, k.cond)
}

// letter samples the directive's booleans.
func (r *runner) letter(c *sim.Ctx, ev *sema.Evaluator) ([]bool, error) {
	a := make([]bool, len(r.dir.bools))
	for i, e := range r.dir.bools {
		v, err := r.value(c, ev, e)
		if err != nil {
			return nil, err
		}
		a[i] = v
	}
	return a, nil
}

func (r *runner) step(c *sim.Ctx, ev *sema.Evaluator) (err error) {
	d := r.dir
	a, err := r.letter(c, ev)
	if err != nil {
		return err
	}
	st := &stepper{}
	defer func() {
		if b := c.Budget(); b != nil && err == nil {
			err = b.Step(st.steps)
		}
	}()
	if d.kind == dCover {
		hit, ss := st.advance(append(slices.Clone(r.ss), d.seq), a)
		r.ss = ss
		if len(ss) > maxStates {
			return r.tooLarge()
		}
		if hit {
			r.hits++
			r.cover(c)
			err = c.TraceEvent(trace.CoverHit, r.message(), d.src)
		}
		return err
	}
	hit, err := r.coverAssert(st, a)
	if err != nil {
		return err
	}
	if hit {
		r.cover(c)
	}
	obls := r.obls
	if !r.ticked {
		obls = conjuncts(d.prop)
	}
	r.ticked = true
	var next []*prop
	seen := map[string]bool{}
	failed := false
	for _, o := range obls {
		for _, x := range st.progress(o, a) {
			switch x.k {
			case pFalse:
				failed = true
			case pTrue:
			default:
				if !seen[x.key] {
					seen[x.key] = true
					next = append(next, x)
				}
			}
		}
	}
	r.obls = next
	if len(next) > maxStates {
		return r.tooLarge()
	}
	if failed {
		err = r.fail(c)
	}
	return err
}

// coverAssert advances the antecedent matches of an uncovered assert
// by the letter a and reports whether the assert is covered at this
// tick: an antecedent match ends here, or this is the first tick of
// an assert without an antecedent.
func (r *runner) coverAssert(st *stepper, a []bool) (bool, error) {
	d := r.dir
	switch {
	case r.covered:
		return false, nil
	case d.ante == nil:
		return true, nil
	}
	starts := r.as
	if d.anteAll || !r.ticked {
		starts = append(slices.Clone(starts), d.ante)
	}
	hit, as := st.advance(starts, a)
	r.as = as
	if len(as) > maxStates {
		return false, r.tooLarge()
	}
	return hit, nil
}

// progress is what the obligation o leaves after the letter a, as
// conjuncts. A top-level always keeps checking after a failing attempt
// (as NVC 1.23 does, so `always b` fails at every tick b is false): the
// attempt of this tick and the always itself stay separate obligations,
// so a false attempt does not absorb the always.
func (st *stepper) progress(o *prop, a []bool) []*prop {
	if o.k == pAlways {
		st.steps++
		return append(slices.Clone(conjuncts(st.prog(o.l, a))), o)
	}
	return conjuncts(st.prog(o, a))
}

func (r *runner) tooLarge() error {
	return errors.New("PSL directive " + r.dir.label + " has more than " +
		strconv.Itoa(maxStates) + " pending obligations")
}

func (r *runner) message() string {
	if r.dir.hasMsg {
		return r.dir.message
	}
	if r.dir.kind == dCover {
		return "cover " + r.dir.label
	}
	return "assertion " + r.dir.label + " failed"
}

// fail reports a violation of the assert. As in NVC 1.23 (#18) a PSL
// assertion is an assertion like any other: the std.env assert state
// applies to it (a severity disabled with SetVhdlAssertEnable drops
// it, an enabled one is counted by GetVhdlAssertCount, the re-run lens
// and the status JSON, and SetVhdlAssertFormat rewrites its
// diagnostic), so it is reported without sim.Report.PSL. An enabled
// violation also sets PslAssertFailed.
func (r *runner) fail(c *sim.Ctx) error {
	sev := r.dir.severity
	if sev == trace.NoSeverity || sev > trace.Failure {
		sev = trace.Note // as Ctx.Report
	}
	if c.AssertEnabled(sev) {
		c.PSLFail()
	}
	return c.Report(sim.Report{Assertion: true, Severity: sev,
		Message: r.message(), Src: r.dir.src})
}

// name is how a cover directive is named in its diagnostics: the
// label and the report text.
func (r *runner) name() string {
	if r.dir.hasMsg {
		return r.dir.label + " (" + r.dir.message + ")"
	}
	return r.dir.label
}

// Finish implements sim.Finisher: cover results, and strong
// obligations still open at the end of the run.
func (r *runner) Finish(c *sim.Ctx) error {
	d := r.dir
	if d.formal != "" || d.unsup != "" {
		return nil
	}
	r.sync(c)
	switch d.kind {
	case dCover:
		if r.hits == 0 {
			c.Diagnostic(d.src, diag.New(diag.SimCoverMiss, r.name()))
			return c.TraceEvent(trace.CoverMiss, r.message(), d.src)
		}
		c.Diagnostic(d.src, diag.New(diag.SimCoverHit, r.name(),
			strconv.Itoa(r.hits)))
	case dAssert:
		if slices.ContainsFunc(r.obls, pending) {
			return r.fail(c)
		}
	}
	return nil
}
