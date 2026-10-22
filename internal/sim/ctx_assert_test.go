package sim

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// assertOp is one step of a generated assert script.
type assertOp struct {
	kind int // 0 report, 1 assert, 2 PSL, 3 enable, 4 clear
	sev  trace.Severity
	on   bool
}

// script is a deterministic pseudo-random assert script of n steps.
func script(seed uint64, n int) []assertOp {
	r := &env.SplitMix64{State: seed}
	out := make([]assertOp, n)
	for i := range out {
		out[i] = assertOp{kind: env.Intn(r, 5),
			sev: trace.Severity(1 + env.Intn(r, 4)),
			on:  env.Intn(r, 3) != 0}
		if out[i].kind == 3 && env.Intn(r, 4) == 0 {
			out[i].sev = trace.NoSeverity // every severity
		}
	}
	return out
}

// TestAssertCountsModel: over generated scripts the kernel's counts,
// trace events and diagnostics match a reference model of #18: only
// enabled assertion violations count; reports and PSL failures never
// do; a disabled severity drops VHDL asserts and reports entirely
// (PSL is unaffected); ClearVhdlAssert zeroes every count.
func TestAssertCountsModel(t *testing.T) {
	for seed := range uint64(40) {
		ops := script(seed, 60)
		var want [nSeverity]uint64
		var off [nSeverity]bool
		events := 0
		for _, o := range ops {
			switch o.kind {
			case 0, 1:
				if !off[o.sev] {
					events++
					if o.kind == 1 {
						want[o.sev]++
					}
				}
			case 2:
				events++
			case 3:
				for s := trace.Note; s <= trace.Failure; s++ {
					if o.sev == trace.NoSeverity || o.sev == s {
						off[s] = !o.on
					}
				}
			case 4:
				want = [nSeverity]uint64{}
			}
		}
		m := &Model{Testbench: "tb"}
		var seen [nSeverity]uint64
		var total uint64
		m.Processes = []Process{{Path: "tb.p",
			Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
				if n > 0 {
					return forever()
				}
				for _, o := range ops {
					var err error
					switch o.kind {
					case 0, 1, 2:
						err = c.Report(Report{Assertion: o.kind != 0,
							PSL: o.kind == 2, Severity: o.sev,
							Message: "m"})
					case 3:
						c.SetAssertEnable(o.sev, o.on)
					case 4:
						c.ClearAsserts()
					}
					if err != nil {
						return Wait{}, err
					}
				}
				for s := trace.Note; s <= trace.Failure; s++ {
					seen[s] = c.AssertCount(s)
				}
				total = c.AssertCount(trace.NoSeverity)
				return forever()
			}}}}
		k, r := run(t, m, Options{StopLevel: trace.Failure + 1})
		if seen != want {
			t.Errorf("seed %d: Ctx counts %v, want %v", seed, seen, want)
		}
		a := k.Asserts()
		got := [nSeverity]uint64{0, a.Note, a.Warning, a.Error, a.Failure}
		if got != want {
			t.Errorf("seed %d: Kernel.Asserts %+v, want %v", seed, a, want)
		}
		if total != a.Total() {
			t.Errorf("seed %d: total %d, want %d", seed, total, a.Total())
		}
		if len(r.events) != events || len(k.Diagnostics()) != events {
			t.Errorf("seed %d: %d events, %d diagnostics, want %d",
				seed, len(r.events), len(k.Diagnostics()), events)
		}
	}
}

// TestAssertDisabledNoHalt: a disabled severity does not halt the run
// at stop_level, and re-enabling it restores reporting.
func TestAssertDisabledNoHalt(t *testing.T) {
	m := &Model{Testbench: "tb"}
	m.Processes = []Process{{Path: "tb.p",
		Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
			if n > 0 {
				return forever()
			}
			c.SetAssertEnable(trace.Error, false)
			if c.AssertEnabled(trace.Error) ||
				!c.AssertEnabled(trace.Warning) {
				t.Error("enable flags")
			}
			if err := c.Report(Report{Assertion: true,
				Severity: trace.Error, Message: "dropped"}); err != nil {
				return Wait{}, err
			}
			return waitFor(1)
		}}}}
	k, r := run(t, m, Options{})
	if k.Status() != Quiet || len(r.events) != 0 ||
		k.Asserts().Total() != 0 {
		t.Errorf("status %v, events %d, asserts %+v", k.Status(),
			len(r.events), k.Asserts())
	}
}

// TestAssertStateCloned: Clone copies the counters, enables, formats
// and read severity, and the two kernels then evolve independently.
func TestAssertStateCloned(t *testing.T) {
	m := &Model{Testbench: "tb"}
	m.Processes = []Process{{Path: "tb.p",
		Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
			if err := c.Report(Report{Assertion: true,
				Severity: trace.Warning, Message: "w"}); err != nil {
				return Wait{}, err
			}
			if n == 0 {
				c.SetAssertEnable(trace.Note, false)
				c.SetReadSeverity(trace.Error)
				if err := c.SetAssertFormat(trace.Warning,
					"{S}: {r}"); err != nil {
					return Wait{}, err
				}
			}
			if n < 3 {
				return waitFor(10)
			}
			return forever()
		}}}}
	k := mustKernel(t, m, Options{})
	k.Run(1)
	c := k.Clone()
	if c.Asserts() != k.Asserts() || c.Asserts().Warning != 1 {
		t.Fatalf("clone %+v, kernel %+v", c.Asserts(), k.Asserts())
	}
	if c.ctx.AssertEnabled(trace.Note) {
		t.Error("clone lost the disabled note severity")
	}
	c.ctx.ClearAsserts()
	c.ctx.SetAssertEnable(trace.Note, true)
	if k.Asserts().Warning != 1 || k.ctx.AssertEnabled(trace.Note) {
		t.Error("clearing the clone changed the original")
	}
	if c.ctx.ReadSeverity() != trace.Error ||
		c.ctx.AssertFormat(trace.Warning) != "{S}: {r}" {
		t.Error("clone lost the read severity or format")
	}
	k.RunAll(100, nil)
	c.RunAll(100, nil)
	if k.Asserts().Warning != 4 || c.Asserts().Warning != 3 {
		t.Errorf("after the run: kernel %+v, clone %+v", k.Asserts(),
			c.Asserts())
	}
}

// TestAssertFormat: the format rewrites the diagnostic message only;
// the trace event keeps the raw message.
func TestAssertFormat(t *testing.T) {
	m := &Model{Testbench: "tb"}
	m.Processes = []Process{{Path: "tb.p",
		Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
			if n == 0 {
				err := c.SetAssertFormat(trace.Error,
					"[{S}] {t:.ns} {i}: {r:*^7}")
				if err != nil {
					return Wait{}, err
				}
				return waitFor(1_500_000)
			}
			return forever0(c.Report(Report{Assertion: true,
				Severity: trace.Error, Message: "hi"}))
		}}}}
	k, r := run(t, m, Options{StopLevel: trace.Failure})
	want := "sim/assert-error: assertion error: [ERROR] 1.5 ns " +
		"tb.p: **hi***"
	if got := codes(k); len(got) != 1 || got[0] != want {
		t.Errorf("diagnostics %q, want %q", got, want)
	}
	if len(r.events) != 1 || r.events[0].Message != "hi" {
		t.Errorf("trace events %+v, want the raw message", r.events)
	}
}

func TestParseFormat(t *testing.T) {
	valid := []string{"", "plain", "{s}", "{S}{r}{t}{i}", "{r:<10}",
		"{r:>3}", "{r:-^9}", "{t:.ps}", "{t:12.sec}", "{t:.hr}",
		"{i:20}", "a {r} b"}
	for _, f := range valid {
		if _, err := parseFormat(f); err != nil {
			t.Errorf("parseFormat(%q): %v", f, err)
		}
	}
	invalid := []string{"{", "{r", "{}", "{x}", "{r.ns}", "{r:.ns}",
		"{t:.parsec}", "{s:abc}", "{rr}"}
	for _, f := range invalid {
		if _, err := parseFormat(f); err == nil {
			t.Errorf("parseFormat(%q) accepted", f)
		}
	}
}

// TestParseFormatErrorQuotesWhole: the error of an invalid format
// quotes the whole format, wherever the bad replacement is (NVC: the
// format as written), never the part left to parse.
func TestParseFormatErrorQuotesWhole(t *testing.T) {
	for _, tc := range []struct{ f, why string }{
		{"CUSTOM {m}", "unknown replacement {m}"},
		{"A {m} tail", "unknown replacement {m}"},
		{"A {r:x5} tail", "bad width in {r:x5}"},
		{"{r} then {", "missing }"},
		{"{s}{t}{i} {q:1} end", "unknown replacement {q:1}"},
	} {
		_, err := parseFormat(tc.f)
		if err == nil {
			t.Errorf("parseFormat(%q) accepted", tc.f)
			continue
		}
		want := "invalid assert format \"" + tc.f + "\": " + tc.why
		if err.Error() != want {
			t.Errorf("parseFormat(%q): %q, want %q", tc.f, err, want)
		}
	}
}

// TestSetAssertFormatInvalid: an invalid format is an error and keeps
// the previous one.
func TestSetAssertFormatInvalid(t *testing.T) {
	k := &Kernel{}
	c := &Ctx{k: k}
	if err := c.SetAssertFormat(trace.Note, "{r}!"); err != nil {
		t.Fatal(err)
	}
	if c.SetAssertFormat(trace.Note, "{q}") == nil ||
		c.AssertFormat(trace.Note) != "{r}!" {
		t.Error("invalid format replaced the old one")
	}
	if c.SetAssertFormat(trace.NoSeverity, "x") == nil {
		t.Error("format without a severity accepted")
	}
	if c.ReadSeverity() != trace.Error {
		t.Errorf("default read severity %v", c.ReadSeverity())
	}
}

func TestFormatTime(t *testing.T) {
	for _, c := range []struct {
		t    Time
		unit string
		want string
	}{
		{0, "", "0 fs"}, {1234, "", "1234 fs"}, {1234, "ps", "1.234 ps"},
		{2_000_000, "ns", "2 ns"}, {1_000_001, "ns", "1.000001 ns"},
		{90e15, "min", "1.5 min"},
	} {
		if got := formatTime(c.t, c.unit); got != c.want {
			t.Errorf("formatTime(%d, %q) = %q, want %q", c.t, c.unit, got,
				c.want)
		}
	}
}

// TestAbort: Abort reports its diagnostic and fails the run after the
// process phase, overriding a stop requested before it.
func TestAbort(t *testing.T) {
	m := &Model{Testbench: "tb"}
	m.Processes = []Process{{Path: "tb.p",
		Code: &fn{f: func(c *Ctx, _ int) (Wait, error) {
			c.Stop(0, false, SrcRef{})
			c.Abort(SrcRef{Path: "a.vhd", Line: 4, Col: 1},
				diag.New(diag.UnsupportedConstruct, "x"))
			return forever()
		}}}}
	k, _ := run(t, m, Options{})
	got := codes(k)
	if k.Status() != Failed || len(got) != 1 ||
		!strings.HasPrefix(got[0], "unsupported/construct") {
		t.Errorf("status %v, diags %q", k.Status(), got)
	}
}

// TestAssertFormatNotPSL: a format set by the design rewrites VHDL
// assertion messages only; a PSL failure keeps its raw message.
func TestAssertFormatNotPSL(t *testing.T) {
	m := &Model{Testbench: "tb"}
	m.Processes = []Process{{Path: "tb.p",
		Code: &fn{f: func(c *Ctx, n int) (Wait, error) {
			if err := c.SetAssertFormat(trace.Error,
				"[{S}] {r}"); err != nil {
				return Wait{}, err
			}
			if err := c.Report(Report{Assertion: true, PSL: true,
				Severity: trace.Error, Message: "psl"}); err != nil {
				return Wait{}, err
			}
			return forever0(c.Report(Report{Assertion: true,
				Severity: trace.Error, Message: "vhdl"}))
		}}}}
	k, _ := run(t, m, Options{StopLevel: trace.Failure})
	got := codes(k)
	if len(got) != 2 || strings.Contains(got[0], "[ERROR]") ||
		!strings.HasSuffix(got[0], "psl") ||
		!strings.HasSuffix(got[1], "[ERROR] vhdl") {
		t.Errorf("diagnostics %q: PSL formatted or VHDL not", got)
	}
}
