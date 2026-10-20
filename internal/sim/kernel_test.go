package sim

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// fn is a Code from a Go function called with the resume count.
type fn struct {
	n int
	f func(c *Ctx, n int) (Wait, error)
	u func(c *Ctx) (bool, error)
}

func (p *fn) Clone() Code { c := *p; return &c }
func (p *fn) Resume(c *Ctx) (Wait, error) {
	p.n++
	return p.f(c, p.n-1)
}
func (p *fn) Until(c *Ctx) (bool, error) {
	if p.u == nil {
		return true, nil
	}
	return p.u(c)
}

func intSig(m *Model, name string, init int64) SigID {
	return m.AddSignal(Signal{Path: "tb." + name, Type: tInt,
		Init: eval.Int(tInt, init)})
}

func iv(v int64) eval.Value { return eval.Int(tInt, v) }

func assign(c *Ctx, s SigID, v, after int64) error {
	return c.Assign(Whole(s), []WaveElem{{Value: iv(v), After: after}},
		Mechanism{})
}

func forever() (Wait, error) { return Wait{}, nil }

func waitFor(d int64) (Wait, error) { return Wait{For: d, HasFor: true}, nil }

func run(t *testing.T, m *Model, o Options) (*Kernel, *rec) {
	t.Helper()
	r := &rec{}
	o.Writer = r
	k := mustKernel(t, m, o)
	k.RunAll(7, nil)
	if k.Err() != nil {
		t.Fatal(k.Err())
	}
	return k, r
}

func wantLines(t *testing.T, r *rec, want ...string) {
	t.Helper()
	got := r.lines()
	if !slices.Equal(got, want) {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"),
			strings.Join(want, "\n"))
	}
}

// TestDeltaChain checks delta numbering: initial values at 0 0, an
// assignment without delay at the next delta, and each process hop one
// more delta; a new time step restarts at delta 0.
func TestDeltaChain(t *testing.T) {
	m := &Model{Testbench: "tb"}
	a, b, c := intSig(m, "a", 0), intSig(m, "b", 0), intSig(m, "c", 0)
	follow := func(src, dst SigID) Code {
		return &fn{f: func(c *Ctx, _ int) (Wait, error) {
			if err := assign(c, dst, c.Value(src).I, 0); err != nil {
				return Wait{}, err
			}
			return Wait{On: []SigID{src}}, nil
		}}
	}
	m.Processes = []Process{
		{Path: "tb.p1", Drivers: []Target{Whole(b)}, Code: follow(a, b)},
		{Path: "tb.p2", Drivers: []Target{Whole(c)}, Code: follow(b, c)},
		{Path: "tb.stim", Drivers: []Target{Whole(a)}},
	}
	m.Processes[2].Code = &fn{f: func(c *Ctx, n int) (Wait, error) {
		switch n {
		case 0:
			return Wait{For: 0, HasFor: true}, assign(c, a, 1, 0)
		case 1:
			return Wait{}, assign(c, a, 2, 10)
		}
		return forever()
	}}
	_, r := run(t, m, Options{})
	wantLines(t, r,
		"0 0 tb.a 0", "0 0 tb.b 0", "0 0 tb.c 0",
		"0 1 tb.a 1", "0 2 tb.b 1", "0 3 tb.c 1",
		"10 0 tb.a 2", "10 1 tb.b 2", "10 2 tb.c 2")
}

// TestProject checks projected-waveform updates against LRM 10.5.2.2
// cases written out by hand.
func TestProject(t *testing.T) {
	mk := func(spec string) []txn {
		var out []txn
		for _, f := range strings.Fields(spec) {
			at, v, _ := strings.Cut(f, ":")
			var a, x int64
			for _, ch := range at {
				a = a*10 + int64(ch-'0')
			}
			for _, ch := range v {
				x = x*10 + int64(ch-'0')
			}
			out = append(out, txn{at: Time(a), v: iv(x)})
		}
		return out
	}
	show := func(ts []txn) string {
		var b []string
		for _, x := range ts {
			b = append(b, strings.TrimSpace(
				strings.Join([]string{itoa(int64(x.at)), itoa(x.v.I)},
					":")))
		}
		return strings.Join(b, " ")
	}
	cases := []struct {
		old, neu  string
		transport bool
		reject    Time
		want      string
	}{
		// transport keeps earlier, drops later-or-equal
		{"5:1 10:0", "7:2", true, 0, "5:1 7:2"},
		{"5:1 10:0", "5:2", true, 0, "5:2"},
		// inertial with reject = delay drops everything in window
		{"5:1", "7:0", false, 7, "7:0"},
		// a same-valued transaction right before the new one survives
		{"5:1", "7:1", false, 7, "5:1 7:1"},
		// only the run adjacent to the new one survives
		{"3:1 4:0 5:1", "7:1", false, 7, "5:1 7:1"},
		// outside the reject window everything is kept
		{"2:1 4:0", "10:1", false, 5, "2:1 4:0 10:1"},
		{"2:1 6:0", "10:1", false, 5, "2:1 10:1"},
		// empty old waveform
		{"", "3:4 6:5", false, 3, "3:4 6:5"},
	}
	for _, c := range cases {
		got := show(project(mk(c.old), mk(c.neu), c.transport, c.reject))
		if got != c.want {
			t.Errorf("project(%q, %q, transport=%v, reject=%d) = %q, "+
				"want %q", c.old, c.neu, c.transport, c.reject, got,
				c.want)
		}
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestDelayMechanisms runs a 2 fs pulse through inertial, transport and
// reject-limited assignments with delay 5.
func TestDelayMechanisms(t *testing.T) {
	m := &Model{Testbench: "tb"}
	src := intSig(m, "src", 0)
	in := intSig(m, "inertial", 0)
	tr := intSig(m, "transport", 0)
	rj := intSig(m, "rejected", 0)
	ok := intSig(m, "kept", 0)
	mech := map[SigID]Mechanism{
		in: {}, tr: {Transport: true},
		rj: {Reject: 3, HasReject: true},
		ok: {Reject: 1, HasReject: true},
	}
	for _, dst := range []SigID{in, tr, rj, ok} {
		m.Processes = append(m.Processes, Process{
			Path: m.Signals[dst].Path + "_p", Drivers: []Target{Whole(dst)},
			Code: &fn{f: func(c *Ctx, _ int) (Wait, error) {
				err := c.Assign(Whole(dst), []WaveElem{{
					Value: c.Value(src), After: 5}}, mech[dst])
				return Wait{On: []SigID{src}}, err
			}}})
	}
	m.Processes = append(m.Processes, Process{Path: "tb.stim",
		Drivers: []Target{Whole(src)},
		Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
			switch n {
			case 0:
				return waitFor(10)
			case 1:
				if err := assign(c, src, 1, 0); err != nil {
					return Wait{}, err
				}
				return waitFor(2)
			case 2:
				return forever0(assign(c, src, 0, 0))
			}
			return forever()
		}}})
	_, r := run(t, m, Options{})
	var got []string
	for _, l := range r.lines() {
		if !strings.HasPrefix(l, "0 0 ") {
			got = append(got, l)
		}
	}
	want := []string{
		"10 1 tb.src 1", "12 1 tb.src 0",
		"15 0 tb.kept 1", "15 0 tb.transport 1",
		"17 0 tb.kept 0", "17 0 tb.transport 0",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"),
			strings.Join(want, "\n"))
	}
}

func forever0(err error) (Wait, error) { return Wait{}, err }

// TestWaitForms covers wait on, until (with and without a matching
// event), for, on+until+for timeouts and wait forever.
func TestWaitForms(t *testing.T) {
	m := &Model{Testbench: "tb"}
	s := intSig(m, "s", 0)
	var log []string
	note := func(c *Ctx, what string) {
		log = append(log, itoa(int64(c.Now()))+"/"+itoa(int64(c.Delta()))+
			" "+what)
	}
	m.Processes = []Process{
		{Path: "tb.stim", Drivers: []Target{Whole(s)},
			Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
				if n < 4 {
					err := assign(c, s, int64(n+1), 0)
					return Wait{For: 10, HasFor: true}, err
				}
				return forever()
			}}},
		{Path: "tb.on", Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
			if n > 0 {
				note(c, "on "+itoa(c.Value(s).I))
			}
			return Wait{On: []SigID{s}}, nil
		}}},
		{Path: "tb.until", Code: &fn{
			f: func(c *Ctx, n int) (Wait, error) {
				if n > 0 {
					note(c, "until "+itoa(c.Value(s).I))
				}
				return Wait{On: []SigID{s}, Until: true}, nil
			},
			u: func(c *Ctx) (bool, error) { return c.Value(s).I == 3, nil },
		}},
		{Path: "tb.timeout", Code: &fn{
			f: func(c *Ctx, n int) (Wait, error) {
				if n > 0 {
					note(c, "timeout "+itoa(c.Value(s).I))
				}
				if n >= 2 {
					return forever()
				}
				return Wait{On: []SigID{s}, Until: true, For: 15,
					HasFor: true}, nil
			},
			u: func(c *Ctx) (bool, error) { return c.Value(s).I == 9, nil },
		}},
	}
	run(t, m, Options{})
	want := []string{
		"0/1 on 1", "10/1 on 2", "15/0 timeout 2",
		"20/1 on 3", "20/1 until 3", "30/0 timeout 3", "30/1 on 4",
	}
	if !slices.Equal(log, want) {
		t.Errorf("log\n%s\nwant\n%s", strings.Join(log, "\n"),
			strings.Join(want, "\n"))
	}
}

// TestPostponed checks a postponed process runs once per time step,
// after the last delta, and sees the settled value.
func TestPostponed(t *testing.T) {
	m := &Model{Testbench: "tb"}
	a, b := intSig(m, "a", 0), intSig(m, "b", 0)
	var seen []string
	m.Processes = []Process{
		{Path: "tb.copy", Drivers: []Target{Whole(b)},
			Code: &fn{f: func(c *Ctx, _ int) (Wait, error) {
				err := assign(c, b, c.Value(a).I*10, 0)
				return Wait{On: []SigID{a}}, err
			}}},
		{Path: "tb.stim", Drivers: []Target{Whole(a)},
			Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
				if n < 2 {
					return waitFor(5)
				}
				if n == 2 {
					return forever0(assign(c, a, 7, 0))
				}
				return forever()
			}}},
		{Path: "tb.mon", Postponed: true,
			Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
				seen = append(seen, itoa(int64(c.Now()))+"/"+
					itoa(int64(c.Delta()))+" a="+itoa(c.Value(a).I)+
					" b="+itoa(c.Value(b).I))
				return Wait{On: []SigID{a, b}}, nil
			}}},
	}
	run(t, m, Options{})
	want := []string{"0/0 a=0 b=0", "10/2 a=7 b=70"}
	if !slices.Equal(seen, want) {
		t.Errorf("postponed saw %v, want %v", seen, want)
	}
	// A postponed process may not cause a delta cycle.
	m2 := &Model{Testbench: "tb"}
	x := intSig(m2, "x", 0)
	m2.Processes = []Process{{Path: "tb.bad", Postponed: true,
		Code: &fn{f: func(c *Ctx, _ int) (Wait, error) {
			return forever0(assign(c, x, 1, 0))
		}}}}
	k, _ := run(t, m2, Options{})
	if k.Status() != Failed {
		t.Errorf("status %v, want failed", k.Status())
	}
}

// TestAttributes checks 'event, 'active, 'last_event, 'last_value,
// 'last_active, 'driving_value and the implicit signals 'stable,
// 'quiet, 'transaction and 'delayed.
func TestAttributes(t *testing.T) {
	m := &Model{Testbench: "tb"}
	s := intSig(m, "s", 0)
	st := m.Implicit(ImplicitStable, s, 3)
	st0 := m.Implicit(ImplicitStable, s, 0)
	qu := m.Implicit(ImplicitQuiet, s, 3)
	tx := m.Implicit(ImplicitTransaction, s, 0)
	dl := m.Implicit(ImplicitDelayed, s, 2)
	if m.Implicit(ImplicitStable, s, 3) != st {
		t.Fatal("implicit signals are not shared")
	}
	var log []string
	m.Processes = []Process{
		{Path: "tb.stim", Drivers: []Target{Whole(s)},
			Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
				switch n {
				case 0:
					return waitFor(10)
				case 1: // event at 10
					return Wait{For: 10, HasFor: true}, assign(c, s, 5, 0)
				case 2: // transaction without event at 20
					return Wait{For: 10, HasFor: true}, assign(c, s, 5, 0)
				case 3:
					dv, err := c.DrivingValue(s)
					if err != nil {
						return Wait{}, err
					}
					log = append(log, "driving_value "+itoa(dv.I))
				}
				return forever()
			}}},
		{Path: "tb.mon", Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
			if n > 0 {
				b := func(x bool) string {
					if x {
						return "T"
					}
					return "F"
				}
				le := c.LastEvent(s)
				leS := "high"
				if le != TimeHigh {
					leS = itoa(le)
				}
				log = append(log, itoa(int64(c.Now()))+"/"+
					itoa(int64(c.Delta()))+
					" ev="+b(c.Event(s))+" act="+b(c.Active(s))+
					" le="+leS+" la="+itoa(c.LastActive(s))+
					" lv="+itoa(c.LastValue(s).I)+
					" st="+b(c.Value(st).Bool())+
					" st0="+b(c.Value(st0).Bool())+
					" qu="+b(c.Value(qu).Bool())+
					" tx="+itoa(c.Value(tx).I)+
					" dl="+itoa(c.Value(dl).I))
			}
			return Wait{On: []SigID{s, st, st0, qu, tx, dl}}, nil
		}}},
	}
	run(t, m, Options{})
	want := []string{
		"10/1 ev=T act=T le=0 la=0 lv=0 st=F st0=F qu=F tx=1 dl=0",
		"10/2 ev=F act=F le=0 la=0 lv=0 st=F st0=T qu=F tx=1 dl=0",
		"12/0 ev=F act=F le=2 la=2 lv=0 st=F st0=T qu=F tx=1 dl=5",
		"13/0 ev=F act=F le=3 la=3 lv=0 st=T st0=T qu=T tx=1 dl=5",
		"20/1 ev=F act=T le=10 la=0 lv=0 st=T st0=T qu=F tx=0 dl=5",
		"23/0 ev=F act=F le=13 la=3 lv=0 st=T st0=T qu=T tx=0 dl=5",
		"driving_value 5",
	}
	if !slices.Equal(log, want) {
		t.Errorf("log\n%s\nwant\n%s", strings.Join(log, "\n"),
			strings.Join(want, "\n"))
	}
}

func codes(k *Kernel) []string {
	var out []string
	for _, d := range k.Diagnostics() {
		out = append(out, string(d.Code)+": "+d.Message)
	}
	return out
}

// TestBudgets checks the time, delta and step budgets end the run with
// sim/budget-exceeded instead of hanging.
func TestBudgets(t *testing.T) {
	clock := func(period int64) *Model {
		m := &Model{Testbench: "tb"}
		s := intSig(m, "clk", 0)
		m.Processes = []Process{{Path: "tb.clk",
			Drivers: []Target{Whole(s)},
			Code: &fn{f: func(c *Ctx, _ int) (Wait, error) {
				err := assign(c, s, 1-c.Value(s).I, period)
				return Wait{On: []SigID{s}}, err
			}}}}
		return m
	}
	k, _ := run(t, clock(5), Options{TimeBudget: 100})
	if k.Status() != Exceeded || k.Now() != 100 {
		t.Errorf("time budget: %v at %d", k.Status(), k.Now())
	}
	if got := codes(k); len(got) != 1 || got[0] !=
		"sim/budget-exceeded: time budget of 100 fs exceeded at 100 fs" {
		t.Errorf("time budget diagnostics %q", got)
	}
	k, _ = run(t, clock(0), Options{DeltaBudget: 50})
	if k.Status() != Exceeded || k.Delta() != 50 {
		t.Errorf("delta budget: %v at delta %d", k.Status(), k.Delta())
	}
	k, _ = run(t, clock(1), Options{StepBudget: 1000})
	if k.Status() != Exceeded || !strings.Contains(codes(k)[0],
		"step budget of 1000 steps") {
		t.Errorf("step budget: %v %q", k.Status(), codes(k))
	}
	// Steps charged before the run (elaboration) count.
	b := eval.NewBudget(1000)
	_ = b.Step(990)
	k, _ = run(t, clock(1), Options{Budget: b})
	if k.Status() != Exceeded || k.Now() > 5 {
		t.Errorf("shared budget: %v at %d", k.Status(), k.Now())
	}
}

// TestCancel checks a cancel between slices stops the run within one
// slice and the trace ends at the current time.
func TestCancel(t *testing.T) {
	m := &Model{Testbench: "tb"}
	s := intSig(m, "clk", 0)
	m.Processes = []Process{{Path: "tb.clk", Drivers: []Target{Whole(s)},
		Code: &fn{f: func(c *Ctx, _ int) (Wait, error) {
			err := assign(c, s, 1-c.Value(s).I, 1)
			return Wait{On: []SigID{s}}, err
		}}}}
	r := &rec{}
	k := mustKernel(t, m, Options{Writer: r})
	slices := 0
	st := k.RunAll(10, func() bool { slices++; return slices > 3 })
	if st != Cancelled || !r.ended || r.end != k.Now() || k.Now() > 31 {
		t.Errorf("cancel: %v ended=%v end=%d now=%d", st, r.ended, r.end,
			k.Now())
	}
}

// TestGlitch checks a zero-width pulse is reported once.
func TestGlitch(t *testing.T) {
	m := &Model{Testbench: "tb"}
	s := intSig(m, "s", 0)
	m.Signals[s].Src = SrcRef{Path: "tb.vhd", Line: 3, Col: 10}
	m.Processes = []Process{{Path: "tb.p", Drivers: []Target{Whole(s)},
		Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
			switch n % 3 {
			case 0:
				return waitFor(10)
			case 1:
				return Wait{For: 0, HasFor: true}, assign(c, s, 1, 0)
			}
			return Wait{For: 0, HasFor: true}, assign(c, s, 0, 0)
		}}}}
	// s'quiet and s'stable pulse for one delta: never a glitch.
	m.Implicit(ImplicitQuiet, s, 0)
	m.Implicit(ImplicitStable, s, 0)
	k, _ := run(t, m, Options{TimeBudget: 50})
	var g []diag.Diagnostic
	for _, d := range k.Diagnostics() {
		if d.Code == diag.SimGlitch {
			g = append(g, d)
		}
	}
	if len(g) != 1 || g[0].Path != "tb.vhd" || g[0].Line != 3 ||
		g[0].Message != "zero-width glitch on tb.s at 10 fs" {
		t.Errorf("glitch diagnostics %+v", g)
	}
}

// TestReports checks assert/report severities, stop_level halting and
// std.env.stop status.
func TestReports(t *testing.T) {
	mk := func(sev trace.Severity) *Model {
		m := &Model{Testbench: "tb"}
		m.Processes = []Process{{Path: "tb.p",
			Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
				if n == 0 {
					err := c.Report(Report{Assertion: true, Severity: sev,
						Message: "boom", Src: SrcRef{Path: "a.vhd",
							Line: 2, Col: 3}})
					return Wait{For: 5, HasFor: true}, err
				}
				return forever0(c.Report(Report{Message: "later"}))
			}}}}
		return m
	}
	for _, c := range []struct {
		sev    trace.Severity
		status Status
		first  string
	}{
		{trace.Note, Quiet, "sim/assert-note: assertion note: boom"},
		{trace.Warning, Quiet,
			"sim/assert-warning: assertion warning: boom"},
		{trace.Error, Halted,
			"sim/assert-error: assertion error: boom"},
		{trace.Failure, Halted,
			"sim/assert-error: assertion failure: boom"},
	} {
		k, r := run(t, mk(c.sev), Options{})
		got := codes(k)
		if k.Status() != c.status || got[0] != c.first {
			t.Errorf("%v: status %v, diags %q", c.sev, k.Status(), got)
		}
		if len(r.events) == 0 || r.events[0].Process != 1 ||
			r.events[0].Line != 2 {
			t.Errorf("%v: events %+v", c.sev, r.events)
		}
		// stop_level changes where the run halts, not the diagnostic.
		k2, _ := run(t, mk(c.sev), Options{StopLevel: trace.Failure})
		if codes(k2)[0] != c.first {
			t.Errorf("stop_level changed the diagnostic: %q", codes(k2))
		}
		if c.sev == trace.Error && k2.Status() != Quiet {
			t.Errorf("stop_level failure halted at error")
		}
	}
	m := &Model{Testbench: "tb"}
	m.Processes = []Process{{Path: "tb.p",
		Code: &fn{f: func(c *Ctx, _ int) (Wait, error) {
			c.Stop(3, false, SrcRef{Path: "a.vhd", Line: 9, Col: 1})
			return waitFor(1)
		}}}}
	k, _ := run(t, m, Options{})
	if k.Status() != Stopped || k.StopStatus() != 3 ||
		codes(k)[0] != "sim/stop-status: std.env.stop called with "+
			"status 3" {
		t.Errorf("stop: %v %d %q", k.Status(), k.StopStatus(), codes(k))
	}
}

// TestForce checks force overrides drivers from the next delta and
// release restores the driven value.
func TestForce(t *testing.T) {
	m := &Model{Testbench: "tb"}
	s := intSig(m, "s", 0)
	m.Processes = []Process{
		{Path: "tb.drv", Drivers: []Target{Whole(s)},
			Code: &fn{f: func(c *Ctx, _ int) (Wait, error) {
				return forever0(assign(c, s, 4, 0))
			}}},
		{Path: "tb.f", Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
			switch n {
			case 0:
				return waitFor(10)
			case 1:
				return Wait{For: 10, HasFor: true}, c.Force(Whole(s), iv(9))
			case 2:
				return forever0(c.Release(Whole(s)))
			}
			return forever()
		}}},
	}
	_, r := run(t, m, Options{})
	wantLines(t, r, "0 0 tb.s 0", "0 1 tb.s 4", "10 1 tb.s 9",
		"20 1 tb.s 4")
}
