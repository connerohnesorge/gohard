package elab

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/token"
)

// o_sweep_test.go holds the tests of the v1.1 "please report" sweep
// (#18 Done when): a legal construct either works or is reported as
// unsupported/construct (please report); none fails with a generic
// "is not static" run-time error or silently.

// oRun is one run of oSim: the OUTPUT lines and every diagnostic of
// elaboration and simulation as "code: message".
type oRun struct {
	out   []string
	diags []string
}

// oAttrActual is the analyzer's rule an attribute actual of a generic
// subprogram breaks today (sema names.go subprogramActual); oSim drops
// it so the interpreters' side of such an actual is tested.
const oAttrActual = "is not a subprogram: a generic subprogram is " +
	"associated with a subprogram"

// oSim analyzes src under std (with the v1.1 detectors bypassed and
// the errors containing one of allow dropped), elaborates top and
// simulates it to the end.
func oSim(t *testing.T, src, top, std string, allow ...string) oRun {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = std
	r := sema.Analyze(sema.Input{Config: cfg, Files: []sema.Source{{
		Path: "t.vhd", Text: []byte(src)}}})
	for _, u := range r.Units {
		u.Opaque, u.Unchecked = false, false
	}
	for _, d := range r.Diagnostics {
		if d.Severity != diag.Error ||
			d.Code == diag.UnsupportedConstruct ||
			d.Code == diag.SemaUnchecked ||
			slices.ContainsFunc(allow, func(a string) bool {
				return strings.Contains(d.Message, a)
			}) {
			continue
		}
		t.Fatalf("analysis: %d:%d: %s", d.Line, d.Col, d.Message)
	}
	st, _ := token.ParseStandard(std)
	var res oRun
	code := Interpreter(InterpOptions{
		Output: func(l string) { res.out = append(res.out, l) },
		Env:    EnvOptions{Standard: st}})
	b := eval.NewBudget(0)
	d, ds := Elaborate(r, Options{Top: top, Budget: b, Code: code})
	for _, x := range ds {
		res.diags = append(res.diags, string(x.Code)+": "+x.Message)
	}
	if d == nil {
		return res
	}
	k, err := sim.New(d.Model, sim.Options{Budget: b,
		TimeBudget: 1_000_000_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	for _, x := range k.Diagnostics() {
		res.diags = append(res.diags, string(x.Code)+": "+x.Message)
	}
	return res
}

// TestOAttrActualSim: a predefined attribute function of a type
// (T'IMAGE, T'VALUE, T'POS, T'SUCC, ...) as the actual of a generic
// subprogram runs in both interpreters: a 2008 subprogram instance and
// a 2019 call with a generic map, statically folded constants and
// process calls, a subtype and a generic type prefix (the latter bound
// in the enclosing instance's frame).
func TestOAttrActualSim(t *testing.T) {
	src := `
use std.textio.all;
entity t is end entity;
architecture a of t is
  procedure say (s : string) is
    variable l : line;
  begin
    write(l, s);
    writeline(output, l);
  end procedure;
  function show generic (type e; function img (x : e) return string)
    (v : e) return string is
  begin
    return "<" & img(v) & ">";
  end function;
  function conv generic (type e; type r;
    function f (x : e) return r) (v : e) return r is
  begin
    return f(v);
  end function;
  function wrap generic (type w) (v : w) return string is
    function si is new show generic map (e => w, img => w'image);
  begin
    return si(v);
  end function;
  subtype small is integer range 0 to 9;
  function si is new show generic map (e => integer, img => integer'image);
  function sb is new show generic map (e => boolean, img => boolean'image);
  function sm is new show generic map (e => small, img => small'image);
  function wi is new wrap generic map (w => character);
  function pos is new conv generic map (e => character, r => integer,
    f => character'pos);
  function val is new conv generic map (e => string, r => integer,
    f => integer'value);
  function nx is new conv generic map (e => small, r => small,
    f => small'succ);
  constant c : string := si(42);
  signal s : integer := 7;
begin
  process
  begin
    say(c & si(s) & sb(s > 3) & sm(4) & wi('x'));
    say(integer'image(pos('A')) & integer'image(val(" 12 "))
      & integer'image(nx(s)));
    say(show generic map (e => bit, img => bit'image) ('1'));
    wait;
  end process;
end architecture;
`
	r := oSim(t, src, "t", "2019", oAttrActual)
	want := []string{"<42><7><true><4><'x'>", "65128", "<'1'>"}
	if len(r.diags) > 0 || !slices.Equal(r.out, want) {
		t.Fatalf("%v %q, want %q", r.diags, r.out, want)
	}
}

// TestOElabNatives: a signal's initial value may call NOW, JUSTIFY,
// the implicit TO_STRING and TO_HSTRING and the assert and PSL state
// functions of std.env: elaboration answers them as the interpreter
// does before any process ran (NVC 1.23 prints the same first line).
func TestOElabNatives(t *testing.T) {
	src := `
use std.textio.all;
use std.env.all;
entity t is end entity;
architecture a of t is
  signal s1 : string(1 to 2) := to_string(42);
  signal s2 : string(1 to 4) := justify("ab", right, 4);
  signal s3 : time := now;
  signal s4 : string(1 to 2) := to_hstring(bit_vector'(x"af"));
  signal n : natural := GetVhdlAssertCount + GetVhdlAssertCount(error);
  signal f : boolean := IsVhdlAssertFailed or PslAssertFailed or
    GetPslCoverAssert;
  signal en : boolean := GetVhdlAssertEnable(note);
  signal rs : severity_level := GetVhdlReadSeverity;
  signal fl : natural := GetVhdlAssertFormat(error)'length;
begin
  process
    variable l : line;
  begin
    write(l, s1 & s2 & time'image(s3) & s4);
    writeline(output, l);
    write(l, integer'image(n) & boolean'image(f) & boolean'image(en) &
      severity_level'image(rs) & integer'image(fl));
    writeline(output, l);
    wait;
  end process;
end architecture;
`
	r := oSim(t, src, "t", "2019")
	want := []string{"42  ab0 fsAF", "0falsetruefailure0"}
	if len(r.diags) > 0 || !slices.Equal(r.out, want) {
		t.Fatalf("%v %q, want %q", r.diags, r.out, want)
	}
}

// TestOElabPleaseReport: what elaboration cannot evaluate in a legal
// design asks for a report, never says "is not static": an impure
// call or a protected method in a signal's initial value, a
// dereference, a std.env function only a running process answers, and
// a generic actual calling an impure function (read by a process: the
// run reports unsupported/construct there).
func TestOElabPleaseReport(t *testing.T) {
	const head = `
use std.env.all;
entity sub is
  generic (g : integer);
end entity;
architecture a of sub is
begin
  process begin report integer'image(g); wait; end process;
end architecture;
use std.env.all;
entity t is end entity;
architecture a of t is
  type pt is protected
    impure function nxt return integer;
  end protected;
  type pt is protected body
    variable c : integer := 0;
    impure function nxt return integer is
    begin c := c + 1; return c; end function;
  end protected body;
  shared variable cnt : pt;
  impure function one return integer is
  begin return 1; end function;
`
	for _, c := range []struct {
		decl, stmt, want string
	}{
		{"signal s : integer := one;", "",
			"impure function calls during elaboration"},
		{"signal s : integer := cnt.nxt;", "",
			"impure function calls during elaboration"},
		{"type ip is access integer;\n" +
			"  impure function mk return ip is\n" +
			"  begin return new integer'(3); end function;\n" +
			"  signal s : integer := mk.all;", "",
			"dereferences during elaboration"},
		{"signal s : boolean := dir_itemexists(\"x\");", "",
			"std.env.DIR_ITEMEXISTS during elaboration"},
		{"", "u : entity work.sub generic map (g => one);",
			"generic actuals that are not static at elaboration"},
	} {
		src := head + "  " + c.decl + "\nbegin\n  " + c.stmt +
			"\nend architecture;\n"
		r := oSim(t, src, "t", "2019")
		want := c.want + " is not supported yet, please report"
		ok := false
		for _, d := range r.diags {
			if strings.Contains(d, "is not static") {
				t.Errorf("%s%s: %s", c.decl, c.stmt, d)
			}
			ok = ok || strings.Contains(d, want)
		}
		if !ok {
			t.Errorf("%s%s: %q, want %q", c.decl, c.stmt, r.diags, want)
		}
	}
}

// TestOPureFunctionAsserts: the assertions and reports of a pure
// function a process calls, and of the procedures it calls, are
// reported at their statements on every call, as the process's own
// would be (they were dropped before), also when a repeated call would
// be memoized and inside a statically run loop; a passing assertion
// reports nothing.
func TestOPureFunctionAsserts(t *testing.T) {
	src := `
entity t is end entity;
architecture a of t is
  procedure say (x : integer) is
  begin
    report "say " & integer'image(x) severity warning;
  end procedure;
  function f (x : integer) return integer is
  begin
    assert x < 2 report "big " & integer'image(x) severity error;
    if x = 7 then say(x); end if;
    return x + 1;
  end function;
  signal s : integer := 3;
begin
  process
    variable v : integer;
  begin
    v := f(1) + f(s) + f(s);
    for i in 1 to 2 loop
      v := v + f(7);
    end loop;
    report integer'image(v);
    wait;
  end process;
end architecture;
`
	r := oSim(t, src, "t", "2019")
	big3 := "sim/assert-error: assertion error: big 3"
	big7 := "sim/assert-error: assertion error: big 7"
	say7 := "sim/assert-warning: report warning: say 7"
	want := []string{big3, big3, big7, say7, big7, say7,
		"sim/assert-note: report note: 26"}
	if !slices.Equal(r.diags, want) {
		t.Fatalf("%q, want %q", r.diags, want)
	}
}

// TestOIllegalSignalUses: the uses of signals that GHDL or NVC reject
// and GoHard's analyzer does not are reported as what they are, not as
// a construct GoHard lacks or a value that is not static: an implicit
// signal ('STABLE) in a subprogram body, and a signal's value or
// attribute read during elaboration.
func TestOIllegalSignalUses(t *testing.T) {
	for _, c := range []struct{ decl, stmt, want string }{
		{`procedure chk is
  begin
    report boolean'image(clk'stable(1 ns));
  end procedure;`, "process begin chk; wait; end process;",
			"the implicit signal 'STABLE cannot be used in a " +
				"subprogram body"},
		{"signal s : bit := clk;", "",
			"the signal clk cannot be read during elaboration"},
		{"signal s : boolean := clk'event;", "",
			"the attribute 'EVENT of a signal cannot be read during " +
				"elaboration"},
	} {
		src := "entity t is end entity;\narchitecture a of t is\n" +
			"  signal clk : bit;\n  " + c.decl + "\nbegin\n  " + c.stmt +
			"\nend architecture;\n"
		r := oSim(t, src, "t", "2019")
		ok := false
		for _, d := range r.diags {
			ok = ok || strings.HasPrefix(d, string(diag.SimRuntimeError)) &&
				strings.Contains(d, c.want)
		}
		if !ok {
			t.Errorf("%s: %q, want a runtime error %q", c.decl, r.diags,
				c.want)
		}
	}
}
