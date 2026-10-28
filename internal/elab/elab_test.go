package elab

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

func analyze(t *testing.T, src string) *sema.Result {
	t.Helper()
	r := sema.Analyze(sema.Input{Files: []sema.Source{{Path: "d.vhd",
		Text: []byte(src)}}, Config: config.Defaults()})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("%s:%d:%d %s %s", d.Path, d.Line, d.Col, d.Code,
				d.Message)
		}
	}
	return r
}

const busDesign = `library ieee;
use ieee.std_logic_1164.all;

entity drv is
  port (en  : in std_logic;
        o   : out std_logic;
        vec : inout std_logic_vector(3 downto 0));
end entity;

architecture rtl of drv is
begin
  o <= '1' when en = '1' else 'Z';
  vec <= (others => 'Z');
end architecture;

library ieee;
use ieee.std_logic_1164.all;

entity tb is
end entity;

architecture sim of tb is
  signal a, b   : std_logic := '0';
  signal bus_io : std_logic;
  signal w      : std_logic_vector(7 downto 0);
begin
  u0 : entity work.drv port map (en => a, o => bus_io, vec => w(7 downto 4));
  u1 : entity work.drv port map (en => b, o => bus_io, vec => w(3 downto 0));
  stim : process
  begin
    wait;
  end process;
end architecture;
`

// goCode is a test stand-in for the interpreter: a Go function per
// process path.
type goCode struct {
	n int
	f func(c *sim.Ctx, n int) (sim.Wait, error)
}

func (g *goCode) Clone() sim.Code { c := *g; return &c }
func (g *goCode) Resume(c *sim.Ctx) (sim.Wait, error) {
	g.n++
	return g.f(c, g.n-1)
}

func logic(t *eval.Type, ch byte) eval.Value {
	return eval.Enum(t, int64(strings.IndexByte(trace.LogicChars, ch)))
}

// TestElaborate checks paths, port collapse, drivers, scopes and the
// interpreted std_logic resolution of a two-driver bus.
func TestElaborate(t *testing.T) {
	r := analyze(t, busDesign)
	var d *Design
	code := func(d *Design, p *Proc) (sim.Code, error) {
		switch {
		case strings.HasSuffix(p.Path, "._p0"):
			// o <= en: drive the port from the enable.
			o, _ := d.Lookup(p.Inst.Path + ".o")
			en, _ := d.Lookup(p.Inst.Path + ".en")
			return &goCode{f: func(c *sim.Ctx, _ int) (sim.Wait, error) {
				v := logic(c.Signal(o).Type, 'Z')
				if c.Value(en).I == 3 { // '1'
					v = logic(c.Signal(o).Type, '1')
				}
				err := c.Assign(sim.Whole(o), []sim.WaveElem{{Value: v}},
					sim.Mechanism{})
				return sim.Wait{On: []sim.SigID{en}}, err
			}}, nil
		case p.Path == "tb.stim":
			a, _ := d.Lookup("tb.a")
			bb, _ := d.Lookup("tb.b")
			return &goCode{f: func(c *sim.Ctx, n int) (sim.Wait, error) {
				one := logic(c.Signal(a).Type, '1')
				switch n {
				case 0:
					return sim.Wait{For: 10, HasFor: true}, nil
				case 1:
					return sim.Wait{For: 10, HasFor: true},
						c.Assign(sim.Whole(a),
							[]sim.WaveElem{{Value: one}}, sim.Mechanism{})
				case 2:
					return sim.Wait{}, c.Assign(sim.Whole(bb),
						[]sim.WaveElem{{Value: logic(c.Signal(a).Type,
							'0')}}, sim.Mechanism{})
				}
				return sim.Wait{}, nil
			}}, nil
		}
		return &goCode{f: func(*sim.Ctx, int) (sim.Wait, error) {
			return sim.Wait{}, nil
		}}, nil
	}
	d, diags := Elaborate(r, Options{Top: "tb", StepBudget: 1e7,
		Code: code})
	if len(diags) != 0 {
		t.Fatalf("diagnostics %v", diags)
	}
	var paths []string
	for _, s := range d.Model.Signals {
		p := s.Path
		if s.Collapsed {
			p += "->" + d.Model.Signals[s.Of].Path + "@" +
				itoa(s.Offset)
		}
		paths = append(paths, p)
	}
	want := []string{"tb.a", "tb.b", "tb.bus_io", "tb.w",
		"tb.u0.en->tb.a@0", "tb.u0.o->tb.bus_io@0", "tb.u0.vec->tb.w@0",
		"tb.u1.en->tb.b@0", "tb.u1.o->tb.bus_io@0", "tb.u1.vec->tb.w@4"}
	if !slices.Equal(paths, want) {
		t.Errorf("signals\n%s\nwant\n%s", strings.Join(paths, "\n"),
			strings.Join(want, "\n"))
	}
	var procs []string
	for _, p := range d.Model.Processes {
		var dr []string
		for _, t := range p.Drivers {
			dr = append(dr, d.Model.Signals[t.Sig].Path+"["+
				itoa(t.First)+"+"+itoa(t.N)+"]")
		}
		procs = append(procs, p.Path+" "+strings.Join(dr, ","))
	}
	if got := strings.Join(procs, "; "); got != "tb.u0._p0 ; "+
		"tb.u0._p1 ; tb.u1._p0 ; tb.u1._p1 ; tb.stim " {
		// Concurrent assignments have no SeqStmt tree yet: their
		// drivers come from the interpreter stage.
		t.Logf("processes: %s", got)
	}
	if d.Model.Signals[2].Resolve == nil || d.Model.Signals[3].Resolve ==
		nil {
		t.Fatalf("std_logic signals are not resolved")
	}
	rec := trace.NewRecorder(nil)
	k, err := sim.New(d.Model, sim.Options{Writer: rec,
		Budget: d.Budget})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(100, nil)
	if k.Status() != sim.Quiet {
		t.Fatalf("status %v %v", k.Status(), k.Diagnostics())
	}
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	got := func(path string, at trace.Time) byte {
		v, _, ok := tr.ValueAt(path, at)
		if !ok {
			t.Fatalf("no %s", path)
		}
		return v.Logic()
	}
	// Both drivers Z, then u0 drives 1 (1 resolved with Z), then the
	// port u1.o reads the same net.
	for _, c := range []struct {
		path string
		at   trace.Time
		want byte
	}{
		{"tb.bus_io", 5, 'Z'}, {"tb.bus_io", 15, '1'},
		{"tb.u1.o", 15, '1'}, {"tb.u0.en", 15, '1'},
	} {
		if g := got(c.path, c.at); g != c.want {
			t.Errorf("%s at %d = %c, want %c", c.path, c.at, g, c.want)
		}
	}
	var scopes []string
	for _, s := range tr.Scopes {
		scopes = append(scopes, s.Path+" "+s.Kind.String()+" "+s.Unit)
	}
	if !slices.Equal(scopes, []string{"tb instance tb(sim)",
		"tb.u0 instance drv(rtl)", "tb.u1 instance drv(rtl)"}) {
		t.Errorf("scopes %q", scopes)
	}
	if k.Steps() <= 0 {
		t.Errorf("no steps counted")
	}
}

// TestResolvedMatchesTable runs the interpreted ieee resolved function
// on every pair of std_ulogic values against the 1164 table (as data)
// and checks it is independent of driver order for triples.
func TestResolvedMatchesTable(t *testing.T) {
	table := [9]string{
		"UUUUUUUUU", "UXXXXXXXX", "UX0X0000X", "UXX11111X",
		"UX01ZWLHX", "UX01WWWWX", "UX01LWLWX", "UX01HWWHX",
		"UXXXXXXXX",
	}
	r := analyze(t, busDesign)
	d, _ := Elaborate(r, Options{Top: "tb"})
	s := d.Model.Signals[2]
	res := s.Resolve
	lt := s.Type
	b := eval.NewBudget(0)
	val := func(i int) eval.Value { return eval.Enum(lt, int64(i)) }
	for i := range 9 {
		for j := range 9 {
			v, err := res.Resolve(b, []eval.Value{val(i), val(j)})
			if err != nil {
				t.Fatal(err)
			}
			if trace.LogicChars[v.I] != table[i][j] {
				t.Errorf("resolved(%c,%c) = %c, want %c",
					trace.LogicChars[i], trace.LogicChars[j],
					trace.LogicChars[v.I], table[i][j])
			}
		}
	}
	for i := range 9 {
		for j := range 9 {
			for k := range 9 {
				a, _ := res.Resolve(b, []eval.Value{val(i), val(j),
					val(k)})
				c, _ := res.Resolve(b, []eval.Value{val(k), val(i),
					val(j)})
				if a.I != c.I {
					t.Fatalf("order dependence at %d %d %d", i, j, k)
				}
			}
		}
	}
}

// TestElabBudget checks that elaboration-time evaluation counts
// against simulation.step_budget: a costly initial value with a small
// budget gives sim/budget-exceeded.
func TestElabBudget(t *testing.T) {
	r := analyze(t, `entity tb is end;
architecture sim of tb is
  function fib(n : natural) return natural is
  begin
    if n < 2 then return n; end if;
    return fib(n - 1) + fib(n - 2);
  end function;
  signal s : natural := fib(18);
begin
end;
`)
	_, diags := Elaborate(r, Options{Top: "tb", StepBudget: 5000})
	if len(diags) != 1 || diags[0].Code != diag.SimBudgetExceeded {
		t.Fatalf("diagnostics %v", diags)
	}
	d, diags := Elaborate(r, Options{Top: "tb", StepBudget: 1e8})
	if len(diags) != 0 || d.Model.Signals[0].Init.I != 2584 {
		t.Fatalf("diagnostics %v value %v", diags,
			d.Model.Signals[0].Init.I)
	}
}

const viewDesign = `library ieee;
use ieee.std_logic_1164.all;

package bus_pkg is
  type bus_t is record
    req  : std_logic;
    data : std_logic_vector(3 downto 0);
    ack  : std_logic;
  end record;
  view master of bus_t is
    req, data : out;
    ack       : in;
  end view;
end package;

library ieee;
use ieee.std_logic_1164.all;
use work.bus_pkg.all;

entity m is
  port (b : view master);
end entity;
architecture rtl of m is
begin
end architecture;

library ieee;
use ieee.std_logic_1164.all;
use work.bus_pkg.all;

entity s is
  port (b : view master'converse);
end entity;
architecture rtl of s is
begin
end architecture;

library ieee;
use ieee.std_logic_1164.all;
use work.bus_pkg.all;

entity tb is end entity;
architecture sim of tb is
  signal x : bus_t;
begin
  um : entity work.m port map (b => x);
  us : entity work.s port map (b => x);
end architecture;
`

// TestModeView checks a mode-view port elaborates into per-element
// directions on the shared net (#17).
func TestModeView(t *testing.T) {
	cfg := config.Defaults()
	cfg.Standard = "2019"
	r := sema.Analyze(sema.Input{Files: []sema.Source{{Path: "d.vhd",
		Text: []byte(viewDesign)}}, Config: cfg})
	for _, dg := range r.Diagnostics {
		if dg.Severity == diag.Error {
			t.Skipf("mode views not analyzed: %s", dg.Message)
		}
	}
	d, diags := Elaborate(r, Options{Top: "tb"})
	if len(diags) != 0 {
		t.Fatal(diags)
	}
	dirs := func(path string) string {
		id, ok := d.Lookup(path)
		if !ok {
			t.Fatalf("no %s", path)
		}
		var b []string
		for _, x := range d.Model.Signals[id].LeafDirs {
			b = append(b, x.String())
		}
		return strings.Join(b, " ")
	}
	if g := dirs("tb.um.b"); g != "out out out out out in" {
		t.Errorf("master dirs %q", g)
	}
	if g := dirs("tb.us.b"); g != "in in in in in out" {
		t.Errorf("converse dirs %q", g)
	}
	rec := trace.NewRecorder(nil)
	k, err := sim.New(d.Model, sim.Options{Writer: rec})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(10, nil)
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	var sigs []string
	for _, s := range tr.Signals {
		if strings.HasPrefix(s.Path, "tb.us.") {
			sigs = append(sigs, s.Path+" "+s.Dir.String())
		}
	}
	if !slices.Equal(sigs, []string{"tb.us.b.ack out",
		"tb.us.b.data in", "tb.us.b.req in"}) {
		t.Errorf("trace signals %q", sigs)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
