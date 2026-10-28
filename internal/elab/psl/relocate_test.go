package psl

import (
	"fmt"
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

// relocate_test.go checks the #66 PSL code paths end to end on random
// simple-subset properties written as VHDL-2008 text (the gp and gs
// generators of engine_test.go): the directive's verdict agrees with
// the reference semantics, and it gives the same events wherever it is
// written: in the architecture, in a vunit bound to it inline or by a
// configuration (use vunit), as an instance of a property declaration
// (with and without formal parameters) and with its sequences as
// instances of sequence declarations.

// pslText prints generated properties and sequences as PSL text. The
// booleans are the signals p0..p2 and their negations n0..n2 (or, in a
// declaration body, its formals x0..x2 and y0..y2); seqs, when set,
// collects a sequence declaration for every sequence operand of a
// property, which the text names instead. With psl, the operands of
// and and or are PSL properties (next[0] b), so two formals never meet
// in one HDL expression, which the simulator does not run.
type pslText struct {
	pos, neg [nBools]string
	seqs     *[]string
	psl      bool
}

func newPSLText(pos, neg string) pslText {
	var t pslText
	for i := range nBools {
		t.pos[i] = pos + strconv.Itoa(i)
		t.neg[i] = neg + strconv.Itoa(i)
	}
	return t
}

func count(lo, hi int) string {
	if hi < 0 {
		return strconv.Itoa(lo) + " to inf"
	}
	return strconv.Itoa(lo) + " to " + strconv.Itoa(hi)
}

// sere prints g as a SERE (valid inside braces).
func (t pslText) sere(g *gs) string {
	switch g.op {
	case "true":
		return "true"
	case "bool":
		return t.pos[g.b]
	case "nbool":
		return t.neg[g.b]
	case "cat":
		return "{" + t.sere(g.l) + "}; {" + t.sere(g.r) + "}"
	case "fuse":
		return "{" + t.sere(g.l) + "} : {" + t.sere(g.r) + "}"
	case "or":
		return "{" + t.sere(g.l) + "} | {" + t.sere(g.r) + "}"
	case "and":
		return "{" + t.sere(g.l) + "} && {" + t.sere(g.r) + "}"
	case "andnl":
		return "{" + t.sere(g.l) + "} & {" + t.sere(g.r) + "}"
	case "star":
		return "{" + t.sere(g.l) + "}[*]"
	case "rep":
		return "{" + t.sere(g.l) + "}[*" + count(g.lo, g.hi) + "]"
	case "goto":
		return t.pos[g.b] + "[->" + count(g.lo, g.hi) + "]"
	case "ncons":
		return t.pos[g.b] + "[=" + count(g.lo, g.hi) + "]"
	case "within":
		return "{" + t.sere(g.l) + "} within {" + t.sere(g.r) + "}"
	}
	panic(g.op)
}

// seq prints g as a sequence operand of a property: braced, or the
// name of a sequence declaration added to t.seqs.
func (t pslText) seq(g *gs) string {
	body := "{" + t.sere(g) + "}"
	if t.seqs == nil {
		return body
	}
	name := "s" + strconv.Itoa(len(*t.seqs))
	*t.seqs = append(*t.seqs, "sequence "+name+" is "+body+";")
	return name
}

// prop prints g as a property.
func (t pslText) prop(g *gp) string {
	b, b2 := t.pos[g.b], t.pos[g.b2]
	bang := ""
	if g.strong {
		bang = "!"
	}
	switch g.op {
	case "bool":
		return b
	case "nbool":
		return t.neg[g.b]
	case "and", "or":
		l, r := t.prop(g.l), t.prop(g.r)
		if t.psl {
			l, r = "next[0] ("+l+")", "next[0] ("+r+")"
		}
		return "(" + l + ") " + g.op + " (" + r + ")"
	case "always":
		return "always (" + t.prop(g.l) + ")"
	case "never":
		return "never " + t.seq(g.s)
	case "eventually":
		return "eventually! " + b
	case "next":
		return "next" + bang + "[" + strconv.Itoa(g.n) + "] (" +
			t.prop(g.l) + ")"
	case "until", "before":
		op := g.op + bang
		if g.incl {
			op += "_"
		}
		return b + " " + op + " " + b2
	case "suffix":
		op := " |=> "
		if g.overlap {
			op = " |-> "
		}
		return t.seq(g.s) + op + "(" + t.prop(g.l) + ")"
	case "abort":
		return "(" + t.prop(g.l) + ") abort " + b
	case "implies":
		return b + " -> (" + t.prop(g.l) + ")"
	case "nextevent":
		return "next_event" + bang + "(" + b + ")[" +
			strconv.Itoa(g.n+1) + "] (" + t.prop(g.l) + ")"
	case "nexteventa", "nexteventE":
		op := "next_event_a"
		if g.op == "nexteventE" {
			op = "next_event_e"
		}
		return op + bang + "(" + b + ")[" + count(g.n+1, g.hi+1) +
			"] (" + t.prop(g.l) + ")"
	}
	panic(g.op)
}

// relocated is the design of one placement of a directive.
type relocated struct {
	name  string
	top   string
	files map[string]string
}

// relocations returns the placements of the directive `assert P` for
// the property g on the word w (one tick per letter).
func relocations(g *gp, w [][]bool) []relocated {
	// next[0] P is P; it makes `assert P` a PSL directive in the
	// architecture also when P is a plain Boolean (a VHDL concurrent
	// assertion otherwise).
	sig := newPSLText("p", "n")
	text := "next[0] (" + sig.prop(g) + ")"
	var seqs []string
	named := sig
	named.seqs = &seqs
	namedText := "next[0] (" + named.prop(g) + ")"
	formal := newPSLText("x", "y")
	formal.psl = true
	var formals, actuals []string
	for i := range nBools {
		formals = append(formals, formal.pos[i], formal.neg[i])
		actuals = append(actuals, sig.pos[i], sig.neg[i])
	}
	params := "property q (boolean " + strings.Join(formals, ", ") +
		") is " + formal.prop(g) + ";"
	call := "q(" + strings.Join(actuals, ", ") + ")"
	design := func(decls, body string) string {
		return relocSrc(w, decls, body)
	}
	vunit := func(binding string) string {
		return "vunit v" + binding + " {\n" +
			"  default clock is rising_edge(clk);\n" +
			"  x: assert " + text + ";\n}\n"
	}
	// v names a block of s too: the vunit's scope is v@work, so the
	// trace has no second scope s.v.
	block := "v: block begin end block;"
	return []relocated{
		{"architecture", "s", map[string]string{
			"s.vhd": design("", "x: assert "+text+";")}},
		{"inline vunit", "s", map[string]string{
			"s.vhd": design("", block), "v.vhd": vunit("(s(rtl))")}},
		{"use vunit", "c", map[string]string{
			"s.vhd": design("", block), "v.vhd": vunit(""),
			"c.vhd": "configuration c of s is\n  use vunit v;\n" +
				"  for rtl\n  end for;\nend configuration;\n"}},
		{"use vunit, incremental binding", "c", map[string]string{
			"s.vhd": design("", ""), "v.vhd": vunit(""),
			"t.vhd": "entity t is end;\narchitecture a of t is\n" +
				"  component sc is end component;\n" +
				"  for u : sc use entity work.s(rtl);\n" +
				"begin\n  u : sc;\nend;\n",
			"c.vhd": "configuration c of t is\n  for a\n" +
				"    for u : sc\n      use vunit v;\n    end for;\n" +
				"  end for;\nend configuration;\n"}},
		{"property instance", "s", map[string]string{
			"s.vhd": design("property q is "+text+";", "x: assert q;")}},
		{"property with formals", "s", map[string]string{
			"s.vhd": design(params, "x: assert "+call+";")}},
		{"sequence instances", "s", map[string]string{
			"s.vhd": design(strings.Join(seqs, "\n  "),
				"x: assert "+namedText+";")}},
	}
}

// relocSrc is the design s(rtl): signals p0..p2 take the letters of w,
// one per clock tick (they change 5 ns before each rising edge), n0..n2
// their negations, with the PSL declarations decls and the statement
// body. The clock stops after the last letter.
func relocSrc(w [][]bool, decls, body string) string {
	var drive strings.Builder
	for _, a := range w {
		for i, v := range a {
			bit := "'0'"
			if v {
				bit = "'1'"
			}
			fmt.Fprintf(&drive, "    p%d <= %s;\n", i, bit)
		}
		drive.WriteString("    wait for 10 ns;\n")
	}
	return `library ieee; use ieee.std_logic_1164.all;
entity s is end;
architecture rtl of s is
  signal clk : std_logic := '0';
  signal p0, p1, p2 : std_logic := '0';
  signal n0, n1, n2 : std_logic := '1';
  default clock is rising_edge(clk);
  ` + decls + `
begin
  clk <= not clk after 5 ns when now < ` + strconv.Itoa(10*len(w)) +
		` ns;
  n0 <= not p0;
  n1 <= not p1;
  n2 <= not p2;
  drive: process begin
` + drive.String() + `    wait;
  end process;
  ` + body + `
end;
`
}

// simFiles analyzes, elaborates and simulates files (top top,
// VHDL-2008) with the real interpreter, running on past error
// assertions, and returns the trace events, the simulation
// diagnostics and the analysis errors.
func simFiles(t *testing.T, top string, files map[string]string,
) ([]trace.Event, []diag.Diagnostic, []diag.Diagnostic) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = "2008"
	var srcs []sema.Source
	for _, p := range slices.Sorted(func(yield func(string) bool) {
		for p := range files {
			if !yield(p) {
				return
			}
		}
	}) {
		srcs = append(srcs, sema.Source{Path: p, Text: []byte(files[p])})
	}
	r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
		Files: srcs})
	var errs []diag.Diagnostic
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	if len(errs) > 0 {
		return nil, nil, errs
	}
	b := eval.NewBudget(0)
	d, ds := elab.Elaborate(r, elab.Options{Top: top, Budget: b,
		Code: Wrap(elab.Interpreter(elab.InterpOptions{}))})
	if d == nil || len(ds) > 0 || len(d.Missing) > 0 {
		t.Fatalf("elaborate: %v", ds)
	}
	rec := trace.NewRecorder(nil)
	k, err := sim.New(d.Model, sim.Options{Budget: b, Writer: rec,
		StopLevel: trace.Failure, TimeBudget: 1_000_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	return tr.Events, k.Diagnostics(), nil
}

// verdicts are the PSL events of a run as kind@time: message.
func verdicts(evs []trace.Event) []string {
	var out []string
	for _, e := range evs {
		switch e.Kind {
		case trace.Assert, trace.CoverHit, trace.CoverMiss:
			out = append(out, fmt.Sprintf("%d@%d: %s", e.Kind, e.Time,
				e.Message))
		}
	}
	return out
}

// TestRelocatedDirective (#66): on random simple-subset properties
// (next_event family included) and random words, `assert P` written
// in the architecture fails exactly when the reference semantics says
// P does not hold on the word, and it gives the same events in a vunit
// bound inline or by use vunit (in a configuration of s, also when s
// has a block named like the vunit, and in a component configuration
// without an entity aspect, whose instance a configuration
// specification binds to s(rtl)), as an instance of a property
// declaration with or without formals, and with its sequences
// declared. simFiles validates every trace.
func TestRelocatedDirective(t *testing.T) {
	r := &rng{env.SplitMix64{State: 66}}
	n := 300
	if testing.Short() {
		n = 60
	}
	for it := range n {
		g := genP(r, 2)
		w := genWord(r, 1+r.n(7))
		var want []string
		for i, rl := range relocations(g, w) {
			evs, ds, errs := simFiles(t, rl.top, rl.files)
			if len(errs) > 0 {
				t.Fatalf("#%d %s: %s\n%v", it, rl.name,
					rl.files["s.vhd"], errs)
			}
			for _, d := range ds {
				switch d.Code {
				case diag.SimAssertError:
				default:
					t.Fatalf("#%d %s: %s %s", it, rl.name, d.Code,
						d.Message)
				}
			}
			got := verdicts(evs)
			if i == 0 {
				want = got
				holds := g.holds(w, 0, len(w), false)
				if failed := len(got) > 0; failed == holds {
					t.Fatalf("#%d %s on %v: failed %v, reference holds "+
						"%v (%v)", it, newPSLText("p", "n").prop(g), w,
						failed, holds, got)
				}
				continue
			}
			if !slices.Equal(got, want) {
				t.Fatalf("#%d %s on %v: %s gives %v, the architecture %v",
					it, newPSLText("p", "n").prop(g), w, rl.name, got,
					want)
			}
		}
	}
}

// TestVunitRecursiveDesign (#66): a recursive design whose generate
// binds its nested instances to its own architecture by a
// configuration specification with use vunit (no import cycle): the
// vunit runs in each of the two nested instances, which fail at the
// 25 ns tick (a = '1', b = '0'), and not in the top one, which no
// indication binds.
func TestVunitRecursiveDesign(t *testing.T) {
	files := map[string]string{
		"props.vhd": `vunit v {
  default clock is rising_edge(clk);
  assert always (a -> b) report "a without b";
}
`,
		"tree.vhd": `library ieee;
use ieee.std_logic_1164.all;
entity tree is
  generic (n : natural := 2);
  port (clk : in std_logic; a, b : in std_logic);
end entity;
architecture rtl of tree is
  component tree is
    generic (n : natural := 2);
    port (clk : in std_logic; a, b : in std_logic);
  end component;
begin
  g : if n > 0 generate
    for all : tree use entity work.tree(rtl);
      use vunit v;
    end for;
  begin
    u : tree generic map (n - 1) port map (clk, a, b);
  end generate;
end architecture;
`,
		"tb.vhd": `library ieee;
use ieee.std_logic_1164.all;
entity tb is end entity;
architecture sim of tb is
  signal clk, a, b : std_logic := '0';
begin
  t : entity work.tree port map (clk, a, b);
  clk <= not clk after 5 ns when now < 40 ns;
  process begin
    wait for 22 ns; a <= '1';
    wait for 10 ns; a <= '0';
    wait;
  end process;
end architecture;
`,
	}
	evs, _, errs := simFiles(t, "tb", files)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	got := verdicts(evs)
	fail := fmt.Sprintf("%d@%d: a without b", trace.Assert, 25*ns)
	want := []string{fail, fail}
	if !slices.Equal(got, want) {
		t.Errorf("events %v, want %v", got, want)
	}
}
