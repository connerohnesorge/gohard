package sim

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

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
	// PSL marks a failed PSL directive (internal/elab/psl): it is
	// reported like an assertion but is not a VHDL assert, so the
	// std.env assert counters, enables and formats ignore it.
	PSL      bool
	Severity trace.Severity
	Message  string
	Src      SrcRef
}

// Report records r in the trace and as a diagnostic (#35: error and
// failure are errors, warning a warning, note a hint) and halts the
// run after the current process phase when the severity reaches
// simulation.stop_level.
//
// The std.env assert state applies to VHDL asserts and reports (not
// to PSL), as in NVC (#18): a severity disabled with
// SetVhdlAssertEnable drops r entirely (no trace event, diagnostic,
// count or halt); an enabled failed assertion is counted; a format
// set with SetVhdlAssertFormat rewrites the diagnostic's message only,
// the trace keeps the raw message.
func (c *Ctx) Report(r Report) error {
	k := c.k
	if r.Severity == trace.NoSeverity || r.Severity > trace.Failure {
		r.Severity = trace.Note
	}
	if !r.PSL && k.assertOff[r.Severity] {
		return nil
	}
	if r.Assertion && !r.PSL {
		k.asserts[r.Severity]++
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
	msg := r.Message
	if f := k.formats[r.Severity]; f != "" && !r.PSL {
		parts, _ := parseFormat(f) // validated when set
		msg = k.applyFormat(parts, r)
	}
	code := diag.SimAssertError
	switch r.Severity {
	case trace.Note:
		code = diag.SimAssertNote
	case trace.Warning:
		code = diag.SimAssertWarning
	}
	args := []string{word, r.Severity.String(), msg}
	if code != diag.SimAssertError {
		args = []string{word, msg}
	}
	k.report(r.Src, diag.New(code, args...))
	if r.Severity >= k.opts.StopLevel && k.stopReq == Running {
		k.stopReq = Halted
	}
	return nil
}

// severity reports whether s is one of note .. failure.
func severity(s trace.Severity) bool {
	return s >= trace.Note && s <= trace.Failure
}

// AssertCount is GetVhdlAssertCount: the enabled assertion violations
// of severity s counted so far, or of every severity for
// trace.NoSeverity.
func (c *Ctx) AssertCount(s trace.Severity) uint64 {
	if s == trace.NoSeverity {
		return c.k.Asserts().Total()
	}
	if !severity(s) {
		return 0
	}
	return c.k.asserts[s]
}

// ClearAsserts is ClearVhdlAssert: every count back to zero.
func (c *Ctx) ClearAsserts() { c.k.asserts = [nSeverity]uint64{} }

// SetAssertEnable is SetVhdlAssertEnable for one severity
// (trace.NoSeverity: every severity).
func (c *Ctx) SetAssertEnable(s trace.Severity, on bool) {
	for x := trace.Note; x <= trace.Failure; x++ {
		if s == trace.NoSeverity || s == x {
			c.k.assertOff[x] = !on
		}
	}
}

// AssertEnabled is GetVhdlAssertEnable.
func (c *Ctx) AssertEnabled(s trace.Severity) bool {
	return severity(s) && !c.k.assertOff[s]
}

// SetAssertFormat is SetVhdlAssertFormat: an invalid format is an
// error (the Valid out parameter is false) and keeps the old one; ""
// restores the plain message. See parseFormat for the syntax.
func (c *Ctx) SetAssertFormat(s trace.Severity, f string) error {
	if !severity(s) {
		return errors.New("assert format of severity " + s.String())
	}
	if _, err := parseFormat(f); err != nil {
		return err
	}
	c.k.formats[s] = f
	return nil
}

// AssertFormat is GetVhdlAssertFormat ("" when none is set).
func (c *Ctx) AssertFormat(s trace.Severity) string {
	if !severity(s) {
		return ""
	}
	return c.k.formats[s]
}

// SetReadSeverity is SetVhdlReadSeverity: the severity of textio
// read failures.
func (c *Ctx) SetReadSeverity(s trace.Severity) {
	if severity(s) {
		c.k.readSev = s
	}
}

// ReadSeverity is GetVhdlReadSeverity (failure until set).
func (c *Ctx) ReadSeverity() trace.Severity {
	if c.k.readSev == trace.NoSeverity {
		return trace.Failure
	}
	return c.k.readSev
}

// Abort reports d at src and ends the run as Failed after the current
// process phase, whatever stop or halt was requested before: the
// outcome of a construct the run cannot continue past, such as a
// built-in subprogram without a native.
func (c *Ctx) Abort(src SrcRef, d diag.Diagnostic) {
	c.k.report(src, d)
	c.k.stopReq = Failed
}

// fmtPart is one part of a parsed assert format: literal text, or a
// replacement {v[:spec]} of variable v.
type fmtPart struct {
	lit   string
	v     byte // 0 for a literal
	fill  rune
	align byte // '<', '>', '^' or 0
	width int
	unit  string // {t:.unit}: the time unit, "" for fs
}

// timeUnits are the units of a {t:.unit} replacement, in fs.
var timeUnits = map[string]int64{"fs": 1, "ps": 1e3, "ns": 1e6,
	"us": 1e9, "ms": 1e12, "sec": 1e15, "min": 60e15, "hr": 3600e15}

// parseFormat parses an assert format (NVC 1.23, #18): literal text
// with replacements {v[:[[fill]align][width][.unit]]}. The variables
// are s and S (the severity in lower and upper case), r (the message),
// t (the simulated time; .unit picks fs, ps, ns, us, ms, sec, min or
// hr, else fs) and i (the instance path of the process). align is <
// (left, the default), > (right) or ^ (centre); a precision is only
// valid on t.
func parseFormat(f string) ([]fmtPart, error) {
	var out []fmtPart
	bad := func(why string) ([]fmtPart, error) {
		return nil, errors.New("invalid assert format " +
			strconv.Quote(f) + ": " + why)
	}
	for f != "" {
		i := strings.IndexByte(f, '{')
		if i < 0 {
			out = append(out, fmtPart{lit: f})
			break
		}
		if i > 0 {
			out = append(out, fmtPart{lit: f[:i]})
		}
		j := strings.IndexByte(f[i:], '}')
		if j < 0 {
			return bad("missing }")
		}
		body := f[i+1 : i+j]
		f = f[i+j+1:]
		if body == "" || !strings.ContainsRune("sSrti", rune(body[0])) {
			return bad("unknown replacement {" + body + "}")
		}
		p := fmtPart{v: body[0], fill: ' '}
		spec := body[1:]
		if spec != "" {
			if spec[0] != ':' {
				return bad("unknown replacement {" + body + "}")
			}
			spec = spec[1:]
		}
		rs := []rune(spec)
		isAlign := func(r rune) bool { return strings.ContainsRune("<>^", r) }
		switch {
		case len(rs) >= 2 && isAlign(rs[1]):
			p.fill, p.align = rs[0], byte(rs[1])
			spec = string(rs[2:])
		case len(rs) >= 1 && isAlign(rs[0]):
			p.align = byte(rs[0])
			spec = string(rs[1:])
		}
		w, unit, dot := strings.Cut(spec, ".")
		if w != "" {
			n, err := strconv.Atoi(w)
			if err != nil || n < 0 || n > 1000 {
				return bad("bad width in {" + body + "}")
			}
			p.width = n
		}
		if dot {
			if p.v != 't' {
				return bad("a precision is only valid on {t}")
			}
			if _, ok := timeUnits[unit]; !ok {
				return bad("unknown time unit " + strconv.Quote(unit))
			}
			p.unit = unit
		}
		out = append(out, p)
	}
	return out, nil
}

// applyFormat expands parts for the report r of the running process.
func (k *Kernel) applyFormat(parts []fmtPart, r Report) string {
	var b strings.Builder
	for _, p := range parts {
		if p.v == 0 {
			b.WriteString(p.lit)
			continue
		}
		var s string
		switch p.v {
		case 's':
			s = r.Severity.String()
		case 'S':
			s = strings.ToUpper(r.Severity.String())
		case 'r':
			s = r.Message
		case 't':
			s = formatTime(k.now, p.unit)
		case 'i':
			if k.current >= 0 {
				s = k.m.Processes[k.current].Path
			}
		}
		b.WriteString(pad(s, p))
	}
	return b.String()
}

// formatTime writes t in unit ("" or "fs": whole femtoseconds), as a
// decimal with at most 15 fraction digits, trailing zeros dropped.
func formatTime(t Time, unit string) string {
	if unit == "" {
		unit = "fs"
	}
	q := new(big.Rat).SetFrac64(int64(t), timeUnits[unit])
	s := q.FloatString(15)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s + " " + unit
}

// pad aligns s in the width of p.
func pad(s string, p fmtPart) string {
	n := p.width - utf8.RuneCountInString(s)
	if n <= 0 {
		return s
	}
	fill := func(m int) string { return strings.Repeat(string(p.fill), m) }
	switch p.align {
	case '>':
		return fill(n) + s
	case '^':
		return fill(n/2) + s + fill(n-n/2)
	}
	return s + fill(n)
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
