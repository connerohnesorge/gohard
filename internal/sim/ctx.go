package sim

import (
	"errors"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// Ctx is a process's view of the kernel while it runs (Code.Resume,
// Condition.Until). It is valid only during that call.
type Ctx struct {
	k *Kernel
}

// Now returns the current simulated time; Delta the delta within it.
func (c *Ctx) Now() Time     { return c.k.now }
func (c *Ctx) Delta() uint32 { return c.k.delta }

// Budget is the step budget the process's evaluation must charge
// (simulation.step_budget).
func (c *Ctx) Budget() *eval.Budget { return c.k.budget }

// Process returns the index of the running process.
func (c *Ctx) Process() int { return int(c.k.current) }

// Signal returns the model of signal s.
func (c *Ctx) Signal(s SigID) *Signal { return &c.k.m.Signals[s] }

// Value returns the current (effective) value of s.
func (c *Ctx) Value(s SigID) eval.Value { return c.k.value(s) }

func (k *Kernel) value(s SigID) eval.Value {
	st := &k.sigs[s]
	if st.cacheOK {
		return st.cache
	}
	flat := make([]eval.Value, len(st.nets))
	for i, n := range st.nets {
		flat[i] = k.nets[n].cur
	}
	v, _ := rebuild(k.m.Signals[s].Init, flat)
	st.cache, st.cacheOK = v, true
	return v
}

// Event reports S'EVENT: an event on s in the current cycle.
func (c *Ctx) Event(s SigID) bool { return c.k.sigs[s].eventCyc == c.k.cyc }

// Active reports S'ACTIVE: a transaction on s in the current cycle.
func (c *Ctx) Active(s SigID) bool {
	return c.k.sigs[s].activeCyc == c.k.cyc
}

// LastEvent returns S'LAST_EVENT in femtoseconds (TimeHigh when s
// never had an event).
func (c *Ctx) LastEvent(s SigID) int64 {
	return c.k.since(s, func(n *net) int64 { return n.lastEvent })
}

// LastActive returns S'LAST_ACTIVE in femtoseconds.
func (c *Ctx) LastActive(s SigID) int64 {
	return c.k.since(s, func(n *net) int64 { return n.lastActive })
}

func (k *Kernel) since(s SigID, at func(*net) int64) int64 {
	last := int64(-1)
	for _, ni := range k.sigs[s].nets {
		last = max(last, at(&k.nets[ni]))
	}
	if last < 0 {
		return TimeHigh
	}
	return int64(k.now) - last
}

// LastValue returns S'LAST_VALUE: each scalar subelement's value
// before its last event (its current value if it never had one).
func (c *Ctx) LastValue(s SigID) eval.Value {
	k := c.k
	st := &k.sigs[s]
	flat := make([]eval.Value, len(st.nets))
	for i, ni := range st.nets {
		flat[i] = k.nets[ni].last
	}
	v, _ := rebuild(k.m.Signals[s].Init, flat)
	return v
}

// Driving reports S'DRIVING for the running process: false when every
// driver of the process on s is disconnected (null).
func (c *Ctx) Driving(s SigID) (bool, error) {
	k := c.k
	any := false
	for j := range k.sigs[s].nets {
		d, ok := k.findDriver(k.current, s, j)
		if !ok {
			return false, errors.New("'driving of " +
				k.m.Signals[s].Path + ": the process has no driver")
		}
		any = any || !k.drivers[d].null
	}
	return any, nil
}

// DrivingValue returns S'DRIVING_VALUE: the current values of the
// running process's drivers of s.
func (c *Ctx) DrivingValue(s SigID) (eval.Value, error) {
	k := c.k
	flat := make([]eval.Value, len(k.sigs[s].nets))
	for j := range flat {
		d, ok := k.findDriver(k.current, s, j)
		if !ok {
			return eval.Value{}, errors.New("'driving_value of " +
				k.m.Signals[s].Path + ": the process has no driver")
		}
		flat[j] = k.drivers[d].cur
	}
	v, _ := rebuild(k.m.Signals[s].Init, flat)
	return v, nil
}

func (k *Kernel) findDriver(p int32, s SigID, j int) (int32, bool) {
	for _, d := range k.nets[k.sigs[s].nets[j]].drivers {
		if k.drivers[d].proc == p {
			return d, true
		}
	}
	return 0, false
}

// Assign executes a signal assignment of the running process to t
// (LRM 10.5.2): every waveform element's value must have the target's
// number of scalar subelements, delays must be non-negative and
// strictly ascending, and the reject limit must not exceed the first
// delay.
func (c *Ctx) Assign(t Target, wave []WaveElem, m Mechanism) error {
	k := c.k
	if k.current < 0 {
		return errors.New("signal assignment outside a process")
	}
	if len(wave) == 0 {
		return errors.New("empty waveform")
	}
	lo, hi, err := k.span(t)
	if err != nil {
		return err
	}
	n := hi - lo
	reject := wave[0].After
	if m.HasReject {
		reject = m.Reject
	}
	if !m.Transport && (reject < 0 || reject > wave[0].After) {
		return errors.New("pulse rejection limit " + strconv.FormatInt(
			reject, 10) + " fs exceeds the first delay")
	}
	flats := make([][]eval.Value, len(wave))
	for i, w := range wave {
		if w.After < 0 {
			return errors.New("negative delay in a waveform")
		}
		if i > 0 && w.After <= wave[i-1].After {
			return errors.New("waveform delays are not ascending")
		}
		if w.Null {
			continue
		}
		flats[i] = flatten(w.Value, nil)
		if len(flats[i]) != n {
			return eval.LengthError(int64(n), int64(len(flats[i])))
		}
	}
	if k.m.Processes[k.current].Postponed && wave[0].After == 0 {
		return errors.New("a postponed process assigns with no delay")
	}
	for j := range n {
		leaf, _ := leafAt(k.m.Signals[t.Sig].Init, lo+j)
		neu := make([]txn, len(wave))
		for i, w := range wave {
			neu[i] = txn{at: k.now + Time(w.After), null: w.Null}
			if !w.Null {
				v, err := eval.Check(flats[i][j], leaf.T)
				if err != nil {
					return err
				}
				neu[i].v = v
			}
		}
		id := k.driverFor(k.current, t.Sig, lo+j)
		d := &k.drivers[id]
		d.wave = project(d.wave, neu, m.Transport, Time(reject))
		for _, x := range neu {
			k.push(x.at, id)
		}
	}
	return nil
}

// Force forces the effective value of t to v from the next delta on
// (LRM 10.5.2.1, force mode in); Release ends a force. ForceMode and
// ReleaseMode (force.go) take the mode explicitly.
func (c *Ctx) Force(t Target, v eval.Value) error {
	return c.force(t, v, false, false)
}

// Release releases a force of mode in on t.
func (c *Ctx) Release(t Target) error {
	return c.force(t, eval.Value{}, true, false)
}

func (c *Ctx) force(t Target, v eval.Value, release, out bool) error {
	k := c.k
	lo, hi, err := k.span(t)
	if err != nil {
		return err
	}
	var flat []eval.Value
	if !release {
		flat = flatten(v, nil)
		if len(flat) != hi-lo {
			return eval.LengthError(int64(hi-lo), int64(len(flat)))
		}
	}
	for j := lo; j < hi; j++ {
		op := forceOp{net: k.sigs[t.Sig].nets[j], release: release,
			out: out}
		if !release {
			op.v = flat[j-lo]
		}
		k.forces = append(k.forces, op)
	}
	return nil
}

// Report is an assert failure or a report statement.
type Report struct {
	// Assertion is true for a failed assert, false for a report.
	Assertion bool
	Severity  trace.Severity
	Message   string
	Src       SrcRef
}

// Report records r in the trace and as a diagnostic (#35: error and
// failure are errors, warning a warning, note a hint) and halts the
// run after the current process phase when the severity reaches
// simulation.stop_level.
func (c *Ctx) Report(r Report) error {
	k := c.k
	if r.Severity == trace.NoSeverity {
		r.Severity = trace.Note
	}
	kind := trace.Report
	word := "report"
	if r.Assertion {
		kind, word = trace.Assert, "assertion"
	}
	if w := k.opts.Writer; w != nil {
		err := w.Event(trace.Event{Time: k.now, Delta: k.delta,
			Kind: kind, Severity: r.Severity, File: r.Src.Path,
			Line: r.Src.Line, Process: int(k.current) + 1,
			Message: r.Message})
		if err != nil {
			k.fail(err)
			return err
		}
	}
	code := diag.SimAssertError
	switch r.Severity {
	case trace.Note:
		code = diag.SimAssertNote
	case trace.Warning:
		code = diag.SimAssertWarning
	}
	args := []string{word, r.Severity.String(), r.Message}
	if code != diag.SimAssertError {
		args = []string{word, r.Message}
	}
	k.report(r.Src, diag.New(code, args...))
	if r.Severity >= k.opts.StopLevel && k.stopReq == Running {
		k.stopReq = Halted
	}
	return nil
}

// Stop is std.env.stop (finish false) or std.env.finish: the run ends
// after the current process phase. A non-zero status is the error
// sim/stop-status.
func (c *Ctx) Stop(status int64, finish bool, src SrcRef) {
	k := c.k
	if status != 0 {
		name := "stop"
		if finish {
			name = "finish"
		}
		k.report(src, diag.New(diag.SimStopStatus, name,
			strconv.FormatInt(status, 10)))
	}
	if k.stopReq == Running || k.stopReq == Halted {
		k.stopReq, k.stopCode = Stopped, status
	}
}

// TraceEvent writes a cover hit or miss (or another event kind) to the
// trace at the current time for the running process: the hook PSL
// directives use. Diagnostics for covers are the PSL runner's.
func (c *Ctx) TraceEvent(kind trace.EventKind, msg string, src SrcRef) error {
	k := c.k
	w := k.opts.Writer
	if w == nil {
		return nil
	}
	err := w.Event(trace.Event{Time: k.now, Delta: k.delta, Kind: kind,
		File: src.Path, Line: src.Line, Process: int(k.current) + 1,
		Message: msg})
	if err != nil {
		k.fail(err)
	}
	return err
}
