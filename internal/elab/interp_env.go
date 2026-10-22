package elab

// interp_env.go holds the std.env natives (LRM 16.5): STOP and
// FINISH, the tool and version queries (one source with conditional
// analysis, condanal.Builtin), GETENV, FILE_NAME/FILE_PATH/FILE_LINE,
// GET_CALL_PATH, the date and time functions and the VHDL assert
// counter-and-format API. The world is EnvOptions: the clock is the
// epoch plus the simulated time in UTC, GETENV sees EnvOptions.Vars,
// and only EnvOptions.Real reads the outside world, through its
// doors (#18). The DIR_* natives work in the run's sandbox (below).
// The PSL natives come later.

import (
	"errors"
	"io/fs"
	"maps"
	"math"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/connerohnesorge/gohard/internal/condanal"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/token"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// DefaultEpoch is the clock of a run whose EnvOptions.Epoch is zero:
// the default of simulation.env.epoch (#28).
var DefaultEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// envFunc evaluates a std.env function.
func (m *machine) envFunc(x *sema.Decl, args []arg, rt *eval.Type,
) (eval.Value, error) {
	name := strings.Trim(string(x.Name), `"`)
	if strings.HasPrefix(name, "dir_") {
		if v, ok, err := m.dirEnv(x, args, rt); ok {
			return v, err
		}
	}
	switch name {
	case "resolution_limit":
		return eval.Phys(rt, 1), nil
	case "tool_type", "tool_vendor", "tool_name", "tool_edition",
		"tool_version", "vhdl_version":
		v, _ := condanal.Builtin(name, m.standard(), m.w.o.Env.ToolVersion)
		return strValue(rt, v), nil
	case "getenv":
		return m.getenv(stringOf(args[0].v), rt), nil
	case "file_name", "file_path", "file_line":
		return m.fileInfo(name, rt), nil
	case "get_call_path":
		return m.callPath(x, rt)
	case "to_string":
		return m.envToString(x, args, rt)
	case "time_to_seconds":
		return eval.RealValue(rt, float64(args[0].v.I)*1e-15), nil
	case "seconds_to_time":
		f := math.Round(args[0].v.F * 1e15)
		if math.IsNaN(f) || f < math.MinInt64 || f >= math.MaxInt64 {
			return eval.Value{}, &eval.Error{Code: eval.ErrCodeOverflow,
				Msg: "SECONDS_TO_TIME(" + secondsText(args[0].v.F) +
					") is outside the range of TIME"}
		}
		return eval.Check(eval.Phys(rt, int64(f)), rt)
	case "epoch", "localtime", "gmtime", "+", "-":
		return m.timeFunc(name, args, rt)
	case "isvhdlassertfailed", "getvhdlassertcount":
		n := m.assertCount(args)
		if name == "getvhdlassertcount" {
			return eval.Check(eval.Int(rt, int64(min(n,
				uint64(math.MaxInt64)))), rt)
		}
		return eval.Bool(n > 0), nil
	case "getvhdlassertenable":
		on := true
		if m.ctx != nil {
			on = m.ctx.AssertEnabled(severityOf(args[0].v))
		}
		return eval.Bool(on), nil
	case "getvhdlassertformat":
		f := ""
		if m.ctx != nil {
			f = m.ctx.AssertFormat(severityOf(args[0].v))
		}
		return strValue(rt, f), nil
	case "getvhdlreadseverity":
		s := trace.Failure
		if m.ctx != nil {
			s = m.ctx.ReadSeverity()
		}
		return eval.Enum(rt, int64(s)-1), nil
	}
	return eval.Value{}, unsupported(x)
}

// envProc executes a std.env procedure; stop reports STOP or FINISH,
// after which the process does not go on.
func (m *machine) envProc(n *node, x *sema.Decl, args []arg,
) (stop bool, err error) {
	if strings.HasPrefix(string(x.Name), "dir_") {
		if ok, err := m.dirProc(x, args); ok {
			return false, err
		}
	}
	switch x.Name {
	case "stop", "finish":
		var status int64
		if len(args) > 0 {
			status = args[0].v.I
		}
		m.ctx.Stop(status, x.Name == "finish", n.src)
		return true, nil
	case "clearvhdlassert":
		m.ctx.ClearAsserts()
		return false, nil
	case "setvhdlassertenable":
		switch len(args) {
		case 1: // (Enable): every severity
			m.ctx.SetAssertEnable(trace.NoSeverity, args[0].v.I != 0)
		case 2: // (Level, Enable)
			m.ctx.SetAssertEnable(severityOf(args[0].v),
				args[1].v.I != 0)
		}
		return false, nil
	case "setvhdlassertformat":
		ferr := m.ctx.SetAssertFormat(severityOf(args[0].v),
			stringOf(args[1].v))
		if len(args) > 2 {
			return false, m.out(args[2], eval.Bool(ferr == nil))
		}
		return false, ferr
	case "setvhdlreadseverity":
		m.ctx.SetReadSeverity(severityOf(args[0].v))
		return false, nil
	}
	return false, unsupported(x)
}

// severityOf is the trace severity of a SEVERITY_LEVEL value.
func severityOf(v eval.Value) trace.Severity {
	return trace.Severity(v.I + 1)
}

// assertCount is GetVhdlAssertCount: with a level the enabled
// assertion violations of that severity, without one those of
// severity warning, error and failure together (as in NVC 1.23).
// IsVhdlAssertFailed is the same count being non-zero.
func (m *machine) assertCount(args []arg) uint64 {
	if m.ctx == nil {
		return 0
	}
	if len(args) > 0 {
		return m.ctx.AssertCount(severityOf(args[0].v))
	}
	return m.ctx.AssertCount(trace.Warning) +
		m.ctx.AssertCount(trace.Error) + m.ctx.AssertCount(trace.Failure)
}

// standard is the standard of the design's files: the value
// VHDL_VERSION gives, the one conditional analysis saw.
func (m *machine) standard() token.Standard {
	if m.w.d.r != nil && m.w.d.r.Standard != 0 {
		return m.w.d.r.Standard
	}
	return m.w.o.Env.Standard
}

// lineOrString returns s as the result of a function returning
// STRING, or LINE (rt an access type: a new heap object).
func (m *machine) lineOrString(rt *eval.Type, s string) eval.Value {
	if rt != nil && rt.Kind == eval.KindAccess {
		return m.w.alloc(rt, eval.Str(s))
	}
	return strValue(rt, s)
}

// getenv is GETENV(name): EnvOptions.Vars, then, under Real only, the
// real environment. An unset name gives "" (STRING) or null (LINE).
// The name is compared exactly, as on POSIX.
func (m *machine) getenv(name string, rt *eval.Type) eval.Value {
	e := m.w.o.Env
	v, ok := e.Vars[name]
	if !ok && e.Real && e.Getenv != nil {
		v, ok = e.Getenv(name)
	}
	if !ok && rt != nil && rt.Kind == eval.KindAccess {
		return eval.Value{T: rt}
	}
	return m.lineOrString(rt, v)
}

// site is a place in the design source: a project-relative (or
// bundle) slash path and a 1-based line.
type site struct {
	path string
	line int
}

// fileName and fileDir are FILE_NAME and FILE_PATH of a site: the
// base name of the file, and its directory relative to the project
// root ("." for the root itself), or absolute under Real when the
// root is known.
func (m *machine) fileName(s site) string { return path.Base(s.path) }

func (m *machine) fileDir(s site) string {
	d := path.Dir(s.path)
	if e := m.w.o.Env; e.Real && e.Root != "" {
		return path.Join(strings.ReplaceAll(e.Root, `\`, "/"), d)
	}
	return d
}

// fileInfo is FILE_NAME, FILE_PATH or FILE_LINE (rt: STRING, LINE or
// POSITIVE) of the call being evaluated.
func (m *machine) fileInfo(name string, rt *eval.Type) eval.Value {
	s := m.callSite(name)
	switch name {
	case "file_name":
		return m.lineOrString(rt, m.fileName(s))
	case "file_path":
		return m.lineOrString(rt, m.fileDir(s))
	}
	if rt != nil && rt.Kind == eval.KindInteger {
		return eval.Int(rt, int64(s.line))
	}
	return m.lineOrString(rt, strconv.Itoa(s.line))
}

// frameFile returns the source file of frame i: the subprogram body's,
// or the process's for the process frame.
func (m *machine) frameFile(i int) *sema.File {
	if i < 0 || i >= len(m.frames) {
		return nil
	}
	if sub := m.frames[i].sub; sub != nil {
		return sub.File
	}
	if i == 0 {
		for _, p := range m.w.d.Procs {
			if p.Index == m.idx {
				return p.File
			}
		}
	}
	return nil
}

// current returns the statement the innermost frame is executing
// (nil while it elaborates its declarations).
func (m *machine) current() *node {
	if len(m.frames) == 0 {
		return nil
	}
	f := &m.frames[len(m.frames)-1]
	if len(f.conts) == 0 {
		return nil
	}
	c := f.conts[len(f.conts)-1]
	if c.i > 0 && c.i <= len(c.ss) {
		return c.ss[c.i-1]
	}
	if c.loop != nil {
		return c.loop
	}
	return nil
}

// callSite locates the call of the std.env function name being
// evaluated: the line of the first occurrence of the name in the
// statement the innermost frame is executing (the statement's own
// line when the name is not written in it), or the line of the call
// of the innermost subprogram while it elaborates its declarations,
// or the process.
func (m *machine) callSite(name string) site {
	fi := len(m.frames) - 1
	if d := m.elab.d; d != nil && len(m.frames) == m.elab.depth &&
		d.Parent != nil {
		// A design object's value (newShared): its declaration.
		if s, ok := declSite(d.File, []*sema.Decl{d}, name); ok {
			return s.valid()
		}
	}
	f := m.frameFile(fi)
	if n := m.current(); n != nil {
		s := site{path: n.src.Path, line: n.src.Line}
		if f != nil && f.Lines != nil && n.s != nil && !n.s.Node.IsZero() {
			for t := range n.s.Node.DescendantTokens() {
				if strings.EqualFold(t.Text(), name) {
					s.line, _ = f.Lines.LineCol(t.Span().Start)
					break
				}
			}
		}
		if s.path == "" && f != nil {
			s.path = f.Path
		}
		return s.valid()
	}
	var r *sema.Region
	if fi > 0 && m.frames[fi].sub != nil {
		r = m.frames[fi].sub.Region
	} else if fi == 0 {
		r = m.prog.region
	}
	var ds []*sema.Decl
	if r != nil {
		ds = r.Decls
	}
	if s, ok := declSite(f, ds, name); ok {
		return s.valid()
	}
	if fi > 0 {
		return m.callOf(fi).valid()
	}
	return m.processSite().valid()
}

// declSite locates the first occurrence of name in the declarations
// ds in file f: a call made while they are elaborated.
func declSite(f *sema.File, ds []*sema.Decl, name string) (site, bool) {
	if f == nil || f.Lines == nil {
		return site{}, false
	}
	for _, d := range ds {
		if d.File != f || d.Node.IsZero() {
			continue
		}
		for t := range d.Node.DescendantTokens() {
			if strings.EqualFold(t.Text(), name) {
				l, _ := f.Lines.LineCol(t.Span().Start)
				return site{path: f.Path, line: l}, true
			}
		}
	}
	return site{}, false
}

// valid makes the line at least 1 (FILE_LINE is POSITIVE).
func (s site) valid() site {
	s.line = max(s.line, 1)
	return s
}

// callOf locates the call that pushed frame i (> 0), in the file of
// frame i-1.
func (m *machine) callOf(i int) site {
	f := m.frameFile(i - 1)
	e := m.frames[i].call
	if f == nil {
		return m.processSite()
	}
	s := site{path: f.Path}
	if e != nil && !e.Node.IsZero() && f.Lines != nil {
		s.line, _ = f.Lines.LineCol(e.Node.Span().Start)
	}
	return s
}

// processSite is the location of the process statement.
func (m *machine) processSite() site {
	ps := m.w.d.Model.Processes
	if m.idx >= 0 && m.idx < len(ps) {
		return site{path: ps[m.idx].Src.Path, line: ps[m.idx].Src.Line}
	}
	return site{}
}

// processName is the upper-case label of the process (_P0 for the
// first unlabeled one), as NVC names it in a call path.
func (m *machine) processName() string {
	ps := m.w.d.Model.Processes
	if m.idx < 0 || m.idx >= len(ps) {
		return ""
	}
	p := ps[m.idx].Path
	if i := strings.LastIndexAny(p, ".:"); i >= 0 {
		p = p[i+1:]
	}
	return strings.ToUpper(p)
}

// callPath is GET_CALL_PATH: a new CALL_PATH_VECTOR, innermost call
// first, of one CALL_PATH_ELEMENT per subprogram being executed and
// one for the process. Each element is the subprogram's upper-case
// name and the place it is executing: the GET_CALL_PATH call for the
// innermost one, the call of the next one for the others.
func (m *machine) callPath(x *sema.Decl, rt *eval.Type) (eval.Value,
	error,
) {
	if x.Type == nil || x.Type.Base().Designated == nil {
		return eval.Value{}, unsupported(x)
	}
	vt, err := m.ev.LayoutIn(x.Type.Base().Designated, m.b)
	if err != nil {
		return eval.Value{}, err
	}
	et := vt.Elem
	if et == nil || et.Kind != eval.KindRecord || len(et.Fields) != 4 {
		return eval.Value{}, unsupported(x)
	}
	var elems []eval.Value
	add := func(name string, s site) {
		s = s.valid()
		elems = append(elems, eval.Record(et, []eval.Value{
			m.w.alloc(et.Fields[0].Type, eval.Str(name)),
			m.w.alloc(et.Fields[1].Type, eval.Str(m.fileName(s))),
			m.w.alloc(et.Fields[2].Type, eval.Str(m.fileDir(s))),
			eval.Int(et.Fields[3].Type, int64(s.line)),
		}))
	}
	top := len(m.frames) - 1
	for i := top; i >= 0; i-- {
		s := m.callSite("get_call_path")
		if i < top {
			s = m.callOf(i + 1)
		}
		switch sub := m.frames[i].sub; {
		case sub != nil:
			add(strings.ToUpper(sub.Text), s)
		case i == 0:
			add(m.processName(), s)
		}
	}
	if len(m.frames) == 0 {
		add(m.processName(), m.processSite())
	}
	if err := m.b.Step(int64(len(elems))); err != nil {
		return eval.Value{}, err
	}
	v := eval.Array(vt, []eval.Range{{Left: 0,
		Right: int64(len(elems)) - 1, Dir: eval.To}}, elems)
	return m.w.alloc(rt, v), nil
}

// envToString is the TO_STRING of std.env: of a TIME_RECORD (ISO
// 8601, FRAC_DIGITS digits of the second) and of call paths
// ("path/file:line:NAME", the elements of a vector joined by
// Separator).
func (m *machine) envToString(x *sema.Decl, args []arg, rt *eval.Type,
) (eval.Value, error) {
	v := args[0].v
	switch typeKey(x.Params[0].Type) {
	case "time_record":
		digits := int64(0)
		if len(args) > 1 {
			digits = args[1].v.I
		}
		t, err := recordTime(v)
		if err != nil {
			return eval.Value{}, err
		}
		return strValue(rt, isoTime(t, int(digits))), nil
	case "call_path_element":
		s, err := m.pathElement(v)
		return strValue(rt, s), err
	case "call_path_vector_ptr":
		if v.I == 0 {
			return strValue(rt, ""), nil
		}
		dv, err := m.w.deref(v)
		if err != nil {
			return eval.Value{}, err
		}
		v = dv
	}
	sep := ""
	if len(args) > 1 {
		sep = stringOf(args[1].v)
	}
	parts := make([]string, len(v.E))
	for i, e := range v.E {
		s, err := m.pathElement(e)
		if err != nil {
			return eval.Value{}, err
		}
		parts[i] = s
	}
	return strValue(rt, strings.Join(parts, sep)), nil
}

// typeKey is the lower-case name of t's base type.
func typeKey(t *sema.Type) string {
	if t == nil || t.Base().Decl == nil {
		return ""
	}
	return string(t.Base().Decl.Name)
}

// pathElement is the TO_STRING of a CALL_PATH_ELEMENT.
func (m *machine) pathElement(v eval.Value) (string, error) {
	if len(v.E) != 4 {
		return "", errors.New("TO_STRING of a malformed call path")
	}
	var s [3]string
	for i := range s {
		if v.E[i].I == 0 {
			continue
		}
		l, err := m.w.deref(v.E[i])
		if err != nil {
			return "", err
		}
		s[i] = stringOf(l)
	}
	return path.Join(s[2], s[1]) + ":" + strconv.FormatInt(v.E[3].I, 10) +
		":" + s[0], nil
}

// now is the current time of the run's clock: the epoch plus the
// simulated time, or the wall clock under Real.
func (m *machine) now() time.Time {
	e := m.w.o.Env
	if e.Real && e.Clock != nil {
		return e.Clock.Now()
	}
	ep := e.Epoch
	if ep.IsZero() {
		ep = DefaultEpoch
	}
	var fs int64
	if m.ctx != nil {
		fs = int64(m.ctx.Now())
	}
	// fs / 1e6 is ns; the remainder is below the clock's resolution.
	return ep.Add(time.Duration(fs / 1e6)).UTC()
}

// local is the zone of LOCALTIME: UTC, or the wall clock's zone
// under Real.
func (m *machine) local() *time.Location {
	if e := m.w.o.Env; e.Real && e.Clock != nil {
		return e.Clock.Now().Location()
	}
	return time.UTC
}

// timeFunc evaluates EPOCH, LOCALTIME, GMTIME and the TIME_RECORD
// operators.
func (m *machine) timeFunc(name string, args []arg, rt *eval.Type,
) (eval.Value, error) {
	var t time.Time
	switch {
	case len(args) == 0:
		t = m.now()
	case args[0].v.Kind() == eval.KindFloating && len(args) == 1:
		var err error
		if t, err = secondsTime(args[0].v.F); err != nil {
			return eval.Value{}, err
		}
	case len(args) == 2 && name != "-" || len(args) == 2 &&
		args[0].v.Kind() != args[1].v.Kind():
		// TREC +/- DELTA, DELTA + TREC, DELTA - TREC (= TREC - DELTA).
		rec, dl := args[0].v, args[1].v
		if rec.Kind() != eval.KindRecord {
			rec, dl = dl, rec
		}
		r, err := recordTime(rec)
		if err != nil {
			return eval.Value{}, err
		}
		d := dl.F
		if name == "-" {
			d = -d
		}
		if t, err = addSeconds(r, d); err != nil {
			return eval.Value{}, err
		}
	case len(args) == 2: // TR1 - TR2
		a, err := recordTime(args[0].v)
		if err != nil {
			return eval.Value{}, err
		}
		b, err := recordTime(args[1].v)
		if err != nil {
			return eval.Value{}, err
		}
		return eval.RealValue(rt, unixSeconds(a)-unixSeconds(b)), nil
	default: // EPOCH, LOCALTIME, GMTIME of a TIME_RECORD
		var err error
		if t, err = recordTime(args[0].v); err != nil {
			return eval.Value{}, err
		}
	}
	switch {
	case rt.Kind == eval.KindFloating:
		return eval.RealValue(rt, unixSeconds(t)), nil
	case name == "localtime":
		t = t.In(m.local())
	default:
		t = t.UTC()
	}
	return timeRecord(rt, t)
}

// unixSeconds is t in seconds since 1970-01-01T00:00:00Z.
func unixSeconds(t time.Time) float64 {
	return float64(t.Unix()) + float64(t.Nanosecond()/1000)*1e-6
}

// secondsTime is the time of s seconds since the Unix epoch, at
// microsecond resolution.
func secondsTime(s float64) (time.Time, error) {
	us := math.Round(s * 1e6)
	if math.IsNaN(us) || math.Abs(us) > 1e17 {
		return time.Time{}, &eval.Error{Code: eval.ErrCodeOverflow,
			Msg: secondsText(s) + " seconds is outside the range of " +
				"TIME_RECORD"}
	}
	return time.UnixMicro(int64(us)).UTC(), nil
}

// addSeconds is t plus d seconds.
func addSeconds(t time.Time, d float64) (time.Time, error) {
	us := math.Round(d * 1e6)
	if math.IsNaN(us) || math.Abs(us) > 1e17 {
		return time.Time{}, &eval.Error{Code: eval.ErrCodeOverflow,
			Msg: "a time increment of " + secondsText(d) + " seconds is " +
				"out of range"}
	}
	return t.Add(time.Duration(us) * time.Microsecond), nil
}

// secondsText formats f for a message.
func secondsText(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// TIME_RECORD element positions.
const (
	trMicro = iota
	trSecond
	trMinute
	trHour
	trDay
	trMonth
	trYear
	trWeekday
	trDayOfYear
	trFields
)

// recordTime is the time a TIME_RECORD value denotes (its weekday and
// day of year are ignored), in UTC.
func recordTime(v eval.Value) (time.Time, error) {
	if len(v.E) != trFields {
		return time.Time{}, errors.New("malformed TIME_RECORD")
	}
	f := func(i int) int { return int(v.E[i].I) }
	return time.Date(f(trYear), time.Month(f(trMonth)+1), f(trDay),
		f(trHour), f(trMinute), f(trSecond), f(trMicro)*1000,
		time.UTC), nil
}

// timeRecord is the TIME_RECORD (of layout rt) of t, in t's zone.
func timeRecord(rt *eval.Type, t time.Time) (eval.Value, error) {
	if rt == nil || len(rt.Fields) != trFields {
		return eval.Value{}, errors.New("malformed TIME_RECORD type")
	}
	vals := [trFields]int64{
		trMicro: int64(t.Nanosecond() / 1000), trSecond: int64(t.Second()),
		trMinute: int64(t.Minute()), trHour: int64(t.Hour()),
		trDay: int64(t.Day()), trMonth: int64(t.Month()) - 1,
		trYear: int64(t.Year()), trWeekday: int64(t.Weekday()),
		trDayOfYear: int64(t.YearDay()) - 1,
	}
	e := make([]eval.Value, trFields)
	for i, x := range vals {
		v, err := eval.Check(eval.Value{T: rt.Fields[i].Type, I: x},
			rt.Fields[i].Type)
		if err != nil {
			return eval.Value{}, err
		}
		e[i] = v
	}
	return eval.Record(rt, e), nil
}

// isoTime is TO_STRING of a TIME_RECORD: YYYY-MM-DDThh:mm:ss, then a
// point and the first digits (0 to 6) of the microseconds.
func isoTime(t time.Time, digits int) string {
	s := t.Format("2006-01-02T15:04:05")
	if digits = min(max(digits, 0), 6); digits > 0 {
		us := strconv.Itoa(1000000 + t.Nanosecond()/1000)[1:]
		s += "." + us[:digits]
	}
	return s
}

// The DIR_* natives (LRM 16.5.6). Paths are relative to the working
// directory, which starts at the project root (EnvOptions.Root, the
// sandbox of env.Overlay) and only DIR_WORKINGDIR changes; it is never
// the process's own (no chdir). Names are root-relative slash paths:
// DIRECTORY.Name and DIR_WORKINGDIR give "." for the root and "a/b"
// below it, the same on every machine. A path outside the sandbox
// (above the root, or inside .gohard) is STATUS_ACCESS_DENIED. Reads
// go through the door, which sees the run's own writes; every write
// (DIR_CREATEDIR, the deletions) lands in the run's overlay, which the
// shell flushes to .gohard/run/<hash>/, so the project itself is never
// changed. A listing is sorted by name, "." and ".." included (as
// NVC lists them), and every listing the run makes of the project is
// recorded (env.Overlay.Listings), the item queries listing the
// directory that holds the item.

// Status positions of the std.env status enumerations.
const (
	openOK, openNotFound, openNoDirectory, openDenied, openError = 0, 1,
		2, 3, 4
	createOK, createExists, createDenied, createError = 0, 1, 2, 3
	deleteOK, deleteNoDirectory, deleteNotEmpty, deleteDenied,
	deleteError = 0, 1, 2, 3, 4
	fileOK, fileNoFile, fileDenied, fileError = 0, 1, 2, 3
)

// DirSeparator is the value of std.env.DIR_SEPARATOR: names are slash
// paths on every host.
const DirSeparator = "/"

// sandbox is a door that maps names into its sandbox (env.Overlay).
type sandbox interface {
	Rel(name string) (string, error)
}

// dirDoor is the door of the DIR_* natives (nil: none, an empty
// read-only project).
func (w *world) dirDoor() env.FS {
	if w.o.Env.FS != nil {
		return w.o.Env.FS
	}
	return w.o.FS
}

// dirRel maps the design's path p to its clean root-relative slash
// path; ok is false when p leaves the sandbox.
func (w *world) dirRel(p string) (string, bool) {
	var r string
	switch {
	case filepath.IsAbs(p) || strings.HasPrefix(p, "/"):
		root := w.o.Env.Root
		if root == "" {
			if sb, ok := w.dirDoor().(sandbox); ok {
				x, err := sb.Rel(p)
				return x, err == nil
			}
			return "", false
		}
		x, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
		if err != nil {
			return "", false
		}
		r = filepath.ToSlash(x)
	default:
		r = path.Join(w.cwd, filepath.ToSlash(p))
	}
	r = path.Clean(r)
	if r == ".." || strings.HasPrefix(r, "../") || r == env.Reserved ||
		strings.HasPrefix(r, env.Reserved+"/") {
		return "", false
	}
	if sb, ok := w.dirDoor().(sandbox); ok {
		x, err := sb.Rel(r)
		return x, err == nil
	}
	return r, true
}

// inCwd is the file name a design's relative name denotes after
// DIR_WORKINGDIR moved the working directory (unchanged at the root,
// and for an absolute name).
func (w *world) inCwd(name string) string {
	if w.cwd == "." || w.cwd == "" || name == "" ||
		filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return name
	}
	return path.Join(w.cwd, filepath.ToSlash(name))
}

// dirStat stats the root-relative path r through the door, after
// listing the directory that holds it (so the answer is recorded).
func (w *world) dirStat(r string) (fs.FileInfo, error) {
	door := w.dirDoor()
	if door == nil {
		if r == "." {
			return nil, nil // the root of an empty project
		}
		return nil, fs.ErrNotExist
	}
	if r != "." {
		_, _ = door.ReadDir(path.Dir(r))
	}
	return door.Stat(r)
}

// dirIsDir reports whether the stat result names a directory.
func dirIsDir(r string, fi fs.FileInfo) bool {
	return r == "." && fi == nil || fi != nil && fi.IsDir()
}

// dirEnv evaluates a DIR_* function; ok is false for another name.
func (m *machine) dirEnv(x *sema.Decl, args []arg, rt *eval.Type,
) (eval.Value, bool, error) {
	name := string(x.Name)
	var st int64
	switch name {
	case "dir_open":
		var err error
		if st, err = m.dirOpen(x, args); err != nil {
			return eval.Value{}, true, err
		}
	case "dir_itemexists", "dir_itemisdir", "dir_itemisfile":
		return eval.Bool(m.dirItem(name, stringOf(args[0].v))), true, nil
	case "dir_workingdir":
		if len(args) == 0 {
			return strValue(rt, m.w.cwd), true, nil
		}
		st = m.dirChdir(stringOf(args[0].v))
	case "dir_createdir":
		st = m.dirCreate(stringOf(args[0].v), len(args) > 1 &&
			args[1].v.I != 0)
	case "dir_deletedir":
		st = m.dirDelete(stringOf(args[0].v), len(args) > 1 &&
			args[1].v.I != 0)
	case "dir_deletefile":
		st = m.fileDelete(stringOf(args[0].v))
	default:
		return eval.Value{}, false, nil
	}
	return eval.Enum(rt, st), true, nil
}

// dirProc executes a DIR_* procedure: the function of the same name,
// its status written to the last parameter; ok is false for another
// name.
func (m *machine) dirProc(x *sema.Decl, args []arg) (bool, error) {
	if x.Name == "dir_close" {
		if len(args) > 0 {
			m.dirClose(args[0].v)
		}
		return true, nil
	}
	if len(args) == 0 {
		return false, nil
	}
	last := args[len(args)-1]
	t := last.v.T
	if t == nil && last.formal != nil {
		var err error
		if t, err = m.ev.LayoutIn(last.formal.Type, m.b); err != nil {
			return true, err
		}
	}
	v, ok, err := m.dirEnv(x, args[:len(args)-1], t)
	if !ok || err != nil {
		return ok, err
	}
	return true, m.out(last, v)
}

// dirOpen is DIR_OPEN(Dir, Path): Dir is written (a null name and
// items unless the status is STATUS_OK).
func (m *machine) dirOpen(x *sema.Decl, args []arg) (int64, error) {
	if len(args) < 2 || len(x.Params) == 0 {
		return openError, unsupported(x)
	}
	dt, err := m.ev.LayoutIn(x.Params[0].Type, m.b)
	if err != nil {
		return openError, err
	}
	// The layouts of DIRECTORY's fields and of what they designate
	// (an access layout leaves its designated subtype to the
	// allocator).
	sd := x.Params[0].Type.Base()
	if dt.Kind != eval.KindRecord || len(dt.Fields) != 2 ||
		len(sd.Fields) != 2 {
		return openError, unsupported(x)
	}
	lt, it := dt.Fields[0].Type, dt.Fields[1].Type
	var vt *eval.Type // LINE_VECTOR
	if ft := sd.Fields[1].Type; ft != nil && ft.Base().Designated != nil {
		if vt, err = m.ev.LayoutIn(ft.Base().Designated, m.b); err != nil {
			return openError, err
		}
	}
	if vt == nil || vt.Kind != eval.KindArray || vt.Elem == nil {
		return openError, unsupported(x)
	}
	st, r, names := m.dirList(stringOf(args[1].v))
	dir := eval.Record(dt, []eval.Value{{T: lt}, {T: it}})
	if st == openOK {
		if err := m.b.Step(int64(1 + len(names))); err != nil {
			return openError, err
		}
		et := vt.Elem // LINE
		elems := make([]eval.Value, len(names))
		for i, n := range names {
			elems[i] = m.w.alloc(et, eval.Str(n))
		}
		vec := eval.Array(vt, []eval.Range{{Left: 0,
			Right: int64(len(names)) - 1, Dir: eval.To}}, elems)
		dir.E[0] = m.w.alloc(lt, eval.Str(r))
		dir.E[1] = m.w.alloc(it, vec)
	}
	return st, m.out(args[0], dir)
}

// dirList lists the design's path p: the status, the root-relative
// name and the sorted item names.
func (m *machine) dirList(p string) (int64, string, []string) {
	r, ok := m.w.dirRel(p)
	if !ok {
		return openDenied, "", nil
	}
	fi, err := m.w.dirStat(r)
	switch {
	case errors.Is(err, fs.ErrPermission):
		return openDenied, "", nil
	case errors.Is(err, fs.ErrNotExist):
		return openNotFound, "", nil
	case err != nil:
		return openError, "", nil
	case !dirIsDir(r, fi):
		return openNoDirectory, "", nil
	}
	names := []string{".", ".."}
	if door := m.w.dirDoor(); door != nil {
		es, err := door.ReadDir(r)
		if err != nil {
			return openError, "", nil
		}
		for _, e := range es {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return openOK, r, names
}

// dirClose is DIR_CLOSE(Dir): its name, its items and their vector are
// deallocated.
func (m *machine) dirClose(d eval.Value) {
	if len(d.E) != 2 {
		return
	}
	if v, err := m.w.deref(d.E[1]); err == nil {
		for _, e := range v.E {
			m.w.free(e)
		}
		m.w.free(d.E[1])
	}
	m.w.free(d.E[0])
}

// dirItem is DIR_ITEMEXISTS, DIR_ITEMISDIR or DIR_ITEMISFILE of the
// design's path p.
func (m *machine) dirItem(name, p string) bool {
	r, ok := m.w.dirRel(p)
	if !ok {
		return false
	}
	fi, err := m.w.dirStat(r)
	if err != nil {
		return false
	}
	switch name {
	case "dir_itemisdir":
		return dirIsDir(r, fi)
	case "dir_itemisfile":
		return fi != nil && fi.Mode().IsRegular()
	}
	return true
}

// dirChdir is DIR_WORKINGDIR(Path): the new working directory.
func (m *machine) dirChdir(p string) int64 {
	r, ok := m.w.dirRel(p)
	if !ok {
		return openDenied
	}
	fi, err := m.w.dirStat(r)
	switch {
	case errors.Is(err, fs.ErrPermission):
		return openDenied
	case errors.Is(err, fs.ErrNotExist):
		return openNotFound
	case err != nil:
		return openError
	case !dirIsDir(r, fi):
		return openNoDirectory
	}
	m.w.cwd = r
	return openOK
}

// dirCreate is DIR_CREATEDIR(Path, Parents): without Parents the
// directory holding the new one must exist.
func (m *machine) dirCreate(p string, parents bool) int64 {
	r, ok := m.w.dirRel(p)
	door := m.w.dirDoor()
	if !ok || door == nil {
		return createDenied
	}
	if _, err := m.w.dirStat(r); err == nil {
		return createExists
	} else if errors.Is(err, fs.ErrPermission) {
		return createDenied
	}
	if !parents {
		fi, err := m.w.dirStat(path.Dir(r))
		if err != nil || !dirIsDir(path.Dir(r), fi) {
			return createError
		}
	}
	switch err := door.MkdirAll(r, 0o755); {
	case errors.Is(err, fs.ErrPermission):
		return createDenied
	case err != nil:
		return createError
	}
	return createOK
}

// dirDelete is DIR_DELETEDIR(Path, Recursive): a directory that is not
// empty goes only with Recursive, its contents first. The project root
// is never deleted.
func (m *machine) dirDelete(p string, recursive bool) int64 {
	r, ok := m.w.dirRel(p)
	door := m.w.dirDoor()
	if !ok || r == "." {
		return deleteDenied
	}
	fi, err := m.w.dirStat(r)
	switch {
	case errors.Is(err, fs.ErrPermission):
		return deleteDenied
	case err != nil || door == nil || !dirIsDir(r, fi):
		return deleteNoDirectory
	}
	es, err := door.ReadDir(r)
	if err != nil {
		return deleteError
	}
	if len(es) > 0 && !recursive {
		return deleteNotEmpty
	}
	if err := m.w.removeTree(door, r); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return deleteDenied
		}
		return deleteError
	}
	return deleteOK
}

// removeTree removes directory r and everything below it, deepest
// first, in name order.
func (w *world) removeTree(door env.FS, r string) error {
	es, err := door.ReadDir(r)
	if err != nil {
		return err
	}
	for _, e := range es {
		c := path.Join(r, e.Name())
		if e.IsDir() {
			err = w.removeTree(door, c)
		} else {
			err = w.removeFile(door, c)
		}
		if err != nil {
			return err
		}
	}
	return door.Remove(r)
}

// removeFile removes file r, and what the run wrote to it from the
// world's own copy (world.disk), so it is not read back.
func (w *world) removeFile(door env.FS, r string) error {
	if err := door.Remove(r); err != nil {
		return err
	}
	for _, k := range slices.Sorted(maps.Keys(w.disk)) {
		if x, ok := w.dirRel(k); ok && x == r {
			delete(w.disk, k)
		}
	}
	return nil
}

// fileDelete is DIR_DELETEFILE(Path); a directory is not a file.
func (m *machine) fileDelete(p string) int64 {
	r, ok := m.w.dirRel(p)
	door := m.w.dirDoor()
	if !ok {
		return fileDenied
	}
	fi, err := m.w.dirStat(r)
	switch {
	case errors.Is(err, fs.ErrPermission):
		return fileDenied
	case err != nil || door == nil || dirIsDir(r, fi):
		return fileNoFile
	}
	switch err := m.w.removeFile(door, r); {
	case errors.Is(err, fs.ErrPermission):
		return fileDenied
	case err != nil:
		return fileError
	}
	return fileOK
}

// envDeferred reports whether d is a deferred constant of std.env
// without a full declaration, whose value the interpreter supplies
// (DIR_SEPARATOR).
func envDeferred(d *sema.Decl) bool {
	return d != nil && d.Kind == sema.DeclConstant && d.Value == nil &&
		d.Name == "dir_separator" && unitOf(d) == "std.env"
}

// envConstant reports whether d is envDeferred and, if so, makes sure
// its shared slot holds the value, so a sharedRef to it (sharedKey{nil,
// d}) loads DirSeparator, a STRING(1 TO 1).
func (m *machine) envConstant(d *sema.Decl) bool {
	if !envDeferred(d) {
		return false
	}
	k := sharedKey{nil, d}
	if _, ok := m.w.shared[k]; ok {
		return true
	}
	var t *eval.Type
	if d.Type != nil {
		t, _ = m.ev.LayoutIn(d.Type, m.b)
	}
	v := strValue(t, DirSeparator)
	if t != nil {
		v.T = t.ArraySubtype(eval.Range{Left: 1, Right: 1, Dir: eval.To})
	}
	m.w.shared[k] = slot{v: v, t: v.T}
	return true
}
