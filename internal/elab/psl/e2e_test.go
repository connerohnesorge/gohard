package psl

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/elab"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// fn is a test stand-in process: the sequential interpreter is not
// this package's, so the VHDL processes of the test designs are
// written in Go.
type fn struct {
	f     func(c *sim.Ctx, n int) (sim.Wait, error)
	calls int
}

func (p *fn) Resume(c *sim.Ctx) (sim.Wait, error) {
	p.calls++
	return p.f(c, p.calls)
}

func (p *fn) Clone() sim.Code { q := *p; return &q }

func analyze(t *testing.T, dir string, files ...string) *sema.Result {
	t.Helper()
	var fs []sema.Source
	for _, p := range files {
		b, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		fs = append(fs, sema.Source{Path: p, Text: b})
	}
	cfg := config.Defaults()
	cfg.Standard = "2008"
	r := sema.Analyze(sema.Input{Files: fs, Config: cfg,
		Jobs: env.Sequential{}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("analysis: %s %s", d.Code, d.Message)
		}
	}
	return r
}

func stdLogic(t *testing.T, d *elab.Design, path string) (sim.SigID,
	*eval.Type,
) {
	t.Helper()
	id, ok := d.Lookup(path)
	if !ok {
		t.Fatalf("no signal %s", path)
	}
	return id, d.Model.Signals[id].Type
}

func lit(t *eval.Type, s string) eval.Value {
	return eval.Enum(t, int64(slices.Index(t.Literals, s)))
}

func assign(c *sim.Ctx, id sim.SigID, v eval.Value, after int64) error {
	return c.Assign(sim.Whole(id), []sim.WaveElem{{Value: v,
		After: after}}, sim.Mechanism{})
}

const ns = 1_000_000

// pslSimple elaborates testdata/corpus/psl_simple with Go stand-ins
// for its VHDL processes and runs it at seed.
func pslSimple(t *testing.T, seed uint64) (*sim.Kernel, *trace.Trace) {
	t.Helper()
	return pslSimpleEnd(t, seed, nil, nil)
}

// pslSimpleEnd is pslSimple with start run by the stopper process at
// time 0, before the design's first PSL clock edge, and end at 100 ns,
// just before it finishes the run.
func pslSimpleEnd(t *testing.T, seed uint64,
	start func(c *sim.Ctx) error, end func(c *sim.Ctx),
) (*sim.Kernel, *trace.Trace) {
	t.Helper()
	r := analyze(t, "../../../testdata/corpus/psl_simple",
		"psl_simple.vhd", "psl_simple_tb.vhd")
	var d *elab.Design
	stub := func(dd *elab.Design, p *elab.Proc) (sim.Code, error) {
		d = dd
		tb := "psl_simple_tb."
		sig := func(n string) (sim.SigID, *eval.Type) {
			return stdLogic(t, dd, n)
		}
		switch strings.TrimPrefix(p.Path, tb) {
		case "_p0": // clk <= not clk after 5 ns
			clk, ty := sig(tb + "clk")
			return &fn{f: func(c *sim.Ctx, _ int) (sim.Wait, error) {
				v := lit(ty, "'1'")
				if c.Value(clk).I == v.I {
					v = lit(ty, "'0'")
				}
				return sim.Wait{On: []sim.SigID{clk}},
					assign(c, clk, v, 5*ns)
			}}, nil
		case "stopper":
			return &fn{f: func(c *sim.Ctx, n int) (sim.Wait, error) {
				if n == 1 {
					var err error
					if start != nil {
						err = start(c)
					}
					return sim.Wait{For: 100 * ns, HasFor: true}, err
				}
				if end != nil {
					end(c)
				}
				c.Stop(0, true, sim.SrcRef{})
				return sim.Wait{}, nil
			}}, nil
		case "dut.count":
			clk, ty := sig(tb + "dut.clk")
			cnt, _ := sig(tb + "dut.n")
			return &fn{f: func(c *sim.Ctx, _ int) (sim.Wait, error) {
				var err error
				if c.Event(clk) && c.Value(clk).I == lit(ty, "'0'").I {
					v := c.Value(cnt)
					v.I++
					err = assign(c, cnt, v, 0)
				}
				return sim.Wait{On: []sim.SigID{clk}}, err
			}}, nil
		case "dut._p0", "dut._p1":
			cnt, _ := sig(tb + "dut.n")
			name, on := "dut.ai", []int64{2, 5}
			if strings.HasSuffix(p.Path, "_p1") {
				name, on = "dut.bi", []int64{3, 5}
			}
			out, ty := sig(tb + name)
			return &fn{f: func(c *sim.Ctx, _ int) (sim.Wait, error) {
				v := lit(ty, "'0'")
				if slices.Contains(on, c.Value(cnt).I) {
					v = lit(ty, "'1'")
				}
				return sim.Wait{On: []sim.SigID{cnt}},
					assign(c, out, v, 0)
			}}, nil
		case "dut._p2", "dut._p3":
			from, to := "dut.ai", "dut.a"
			if strings.HasSuffix(p.Path, "_p3") {
				from, to = "dut.bi", "dut.b"
			}
			in, _ := sig(tb + from)
			out, _ := sig(tb + to)
			return &fn{f: func(c *sim.Ctx, _ int) (sim.Wait, error) {
				return sim.Wait{On: []sim.SigID{in}},
					assign(c, out, c.Value(in), 0)
			}}, nil
		}
		return nil, nil
	}
	d, ds := elab.Elaborate(r, elab.Options{Top: "psl_simple_tb",
		Code: Wrap(stub)})
	if len(ds) > 0 || len(d.Missing) > 0 {
		t.Fatalf("elaborate: %v missing %v", ds, d.Missing)
	}
	rec := trace.NewRecorder(nil)
	opts := sim.Options{Seed: seed, Writer: rec, StopLevel: trace.Failure}
	if seed != 0 {
		opts.Order = env.SeedOrder{Seed: seed}
	}
	k, err := sim.New(d.Model, opts)
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if k.Err() != nil {
		t.Fatal(k.Err())
	}
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	return k, tr
}

// TestPSLSimple: exactly one failing clocked assert (55 ns), one cover
// hit (35 ns) and one cover miss, as diagnostics and as timestamped
// trace events tied to the directive's source line, at every seed.
func TestPSLSimple(t *testing.T) {
	var first []byte
	for seed := range uint64(4) {
		k, tr := pslSimple(t, seed)
		if k.Now() != 100*ns {
			t.Fatalf("seed %d: ended at %d", seed, k.Now())
		}
		var got []string
		for _, d := range k.Diagnostics() {
			got = append(got, string(d.Code)+" "+d.Severity.String()+
				" "+itoa(d.Line)+" "+d.Message)
		}
		want := []string{
			"sim/assert-error error 38 assertion error: a and b high " +
				"in the same cycle",
			"sim/cover-hit hint 41 cover a_then_b (a then b) hit 1 times",
			"sim/cover-miss warning 44 cover b_twice (b twice in a " +
				"row) was never hit",
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("seed %d diagnostics:\n%s", seed,
				strings.Join(got, "\n"))
		}
		var evs []string
		for _, e := range tr.Events {
			evs = append(evs, itoa(int(e.Time/ns))+"ns "+
				e.Kind.String()+" "+e.File+":"+itoa(e.Line)+" "+
				e.Message)
		}
		wantEv := []string{
			"35ns cover-hit psl_simple.vhd:41 a then b",
			"55ns assert psl_simple.vhd:38 a and b high in the same cycle",
			"100ns cover-miss psl_simple.vhd:44 b twice in a row",
		}
		if !slices.Equal(evs, wantEv) {
			t.Fatalf("seed %d events:\n%s", seed, strings.Join(evs, "\n"))
		}
		text := trace.Encode(tr)
		if seed == 0 {
			first = text
		} else if string(text) != string(first) {
			t.Fatalf("seed %d: trace differs from seed 0", seed)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestPSLIsAVHDLAssert (#18): as in NVC 1.23, the failing PSL assert
// of psl_simple is an assertion violation like a VHDL one: counted by
// the std.env assert counters (so by the re-run lens and the status
// JSON), dropped with no diagnostic and no count once its severity is
// disabled with SetVhdlAssertEnable, and it sets PslAssertFailed only
// when enabled.
func TestPSLIsAVHDLAssert(t *testing.T) {
	vhdl := func(c *sim.Ctx) error {
		return c.Report(sim.Report{Assertion: true,
			Severity: trace.Error, Message: "vhdl",
			Src: sim.SrcRef{Path: "psl_simple_tb.vhd", Line: 1, Col: 1}})
	}
	cases := []struct {
		name   string
		start  func(c *sim.Ctx) error
		count  uint64 // error asserts counted
		psl    int    // PSL assert diagnostics
		vhdl   bool   // the VHDL assert reported
		failed bool   // PslAssertFailed at the end
	}{
		{"psl only", nil, 1, 1, false, true},
		{"psl and vhdl", vhdl, 2, 1, true, true},
		{"error disabled", func(c *sim.Ctx) error {
			c.SetAssertEnable(trace.Error, false)
			return vhdl(c)
		}, 0, 0, false, false},
	}
	for _, tc := range cases {
		var failed bool
		k, _ := pslSimpleEnd(t, 0, tc.start, func(c *sim.Ctx) {
			failed = c.PSLFailed()
		})
		if a := k.Asserts(); a.Error != tc.count || a.Total() != tc.count {
			t.Errorf("%s: asserts %+v, want %d error", tc.name, a,
				tc.count)
		}
		psl, v := 0, 0
		for _, d := range k.Diagnostics() {
			if d.Code != diag.SimAssertError {
				continue
			}
			if strings.HasSuffix(d.Message, "a and b high in the "+
				"same cycle") {
				psl++
			} else {
				v++
			}
		}
		if psl != tc.psl || (v == 1) != tc.vhdl || v > 1 {
			t.Errorf("%s: %d psl and %d vhdl assert diagnostics",
				tc.name, psl, v)
		}
		if failed != tc.failed {
			t.Errorf("%s: PslAssertFailed %v, want %v", tc.name, failed,
				tc.failed)
		}
	}
}

const formsSrc = `library ieee;
use ieee.std_logic_1164.all;
entity q is end entity;
architecture a of q is
  signal clk, x, y : std_logic := '0';
begin
  f2 : assert always x;
  f3 : assert EF x @ rising_edge(clk);
  f4 : assert A [x U y] @ rising_edge(clk);
  n1 : assume always x @ rising_edge(clk);
  n2 : restrict {x[*]; y};
  n3 : cover {x; y};
  c1 : assert always {x; y[*2]} |=> {y[+]; x[->2]} @ rising_edge(clk);
end architecture;
`

// TestDirectiveForms: unclocked directives and branching-time
// operators get sim/needs-formal (once, at time 0), assume and restrict
// do nothing, and a clocked simple-subset assert compiles and runs.
func TestDirectiveForms(t *testing.T) {
	cfg := config.Defaults()
	cfg.Standard = "2008"
	r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
		Files: []sema.Source{{Path: "q.vhd", Text: []byte(formsSrc)}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("analysis: %s %s", d.Code, d.Message)
		}
	}
	d, ds := elab.Elaborate(r, elab.Options{Top: "q", Code: Wrap(nil)})
	if len(ds) > 0 || len(d.Missing) > 0 {
		t.Fatalf("elaborate: %v missing %v", ds, d.Missing)
	}
	k, err := sim.New(d.Model, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	var got []string
	for _, x := range k.Diagnostics() {
		got = append(got, string(x.Code)+" "+strconv.Itoa(x.Line)+" "+
			x.Message)
	}
	want := []string{
		"sim/needs-formal 7 the unclocked assert f2 needs formal " +
			"verification and does not run in simulation",
		"sim/needs-formal 8 the branching-time operator EF needs " +
			"formal verification and does not run in simulation",
		"sim/needs-formal 9 the branching-time operator A needs " +
			"formal verification and does not run in simulation",
		"sim/needs-formal 12 the unclocked cover n3 needs formal " +
			"verification and does not run in simulation",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("diagnostics:\n%s", strings.Join(got, "\n"))
	}
}
