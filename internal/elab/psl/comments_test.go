package psl

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/elab"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
)

// commentsSrc is the #109 reproducer: under psl.comments the default
// clock and a property declaration sit in the statement part.
const commentsSrc = `library ieee; use ieee.std_logic_1164.all;
entity t is end;
architecture rtl of t is
  signal clk, a, b : std_logic := '0';
begin
  clk <= not clk after 5 ns;
  a <= '1' after 20 ns;
  b <= '1' after 30 ns;
  -- psl default clock is rising_edge(clk);
  -- psl x: assert always (a -> next b);
  -- psl sequence s is {a; b};
  stopper: process begin wait for 100 ns; std.env.finish; end process;
  process begin wait; end process;
end;
`

// TestPSLCommentDefaultClock (#109): a `-- psl` default clock clocks
// the later `-- psl` directive, and no `-- psl` declaration becomes a
// process or takes an unlabeled number: the last process is _p3, as
// NVC 1.23.0 numbers it past the default clock (NVC rejects the
// sequence declaration in the statement part). With `next b` changed
// to `next (not b)` the directive fails at its own line.
func TestPSLCommentDefaultClock(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"holds", commentsSrc, nil},
		{"fails", strings.Replace(commentsSrc, "next b", "next (not b)",
			1), []string{"sim/assert-error 10 assertion error: " +
			"assertion x failed"}},
	}
	for _, tc := range cases {
		paths, got := simComments(t, tc.name, tc.src)
		wantPaths := []string{"t._p0", "t._p1", "t._p2", "t.x",
			"t.stopper", "t._p3"}
		if !slices.Equal(paths, wantPaths) {
			t.Errorf("%s: processes %v, want %v", tc.name, paths,
				wantPaths)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: diagnostics:\n%s", tc.name,
				strings.Join(got, "\n"))
		}
	}
}

// TestPSLCommentDefaultClockOrder (#109): a `-- psl` default clock
// clocks only the directives after it, as GHDL and NVC read it: the
// directive written before it is unclocked (GHDL 6.0.0 reports "no
// clock for PSL directive", NVC 1.23.0 "unclocked properties are not
// supported") and does not run in simulation.
func TestPSLCommentDefaultClockOrder(t *testing.T) {
	src := strings.Replace(commentsSrc,
		"  -- psl default clock is rising_edge(clk);\n"+
			"  -- psl x: assert always (a -> next b);\n",
		"  -- psl x: assert always (a -> next (not b));\n"+
			"  -- psl default clock is rising_edge(clk);\n", 1)
	_, got := simComments(t, "order", src)
	want := []string{"sim/needs-formal 9 the unclocked assert x " +
		"needs formal verification and does not run in simulation"}
	if !slices.Equal(got, want) {
		t.Errorf("diagnostics:\n%s", strings.Join(got, "\n"))
	}
}

// simComments analyzes, elaborates and simulates src (top t) with
// psl.comments on and returns the process paths and the simulation
// diagnostics as "code line message".
func simComments(t *testing.T, name, src string) ([]string, []string) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = "2008"
	cfg.PSL.Comments = true
	r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
		Files: []sema.Source{{Path: "t.vhd", Text: []byte(src)}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("%s: analysis: %s %s", name, d.Code, d.Message)
		}
	}
	b := eval.NewBudget(0)
	d, ds := elab.Elaborate(r, elab.Options{Top: "t", Budget: b,
		Code: Wrap(elab.Interpreter(elab.InterpOptions{}))})
	if d == nil || len(ds) > 0 || len(d.Missing) > 0 {
		t.Fatalf("%s: elaborate: %v missing %v", name, ds, d.Missing)
	}
	var paths []string
	for _, p := range d.Procs {
		paths = append(paths, p.Path)
	}
	k, err := sim.New(d.Model, sim.Options{Budget: b,
		TimeBudget: 1_000_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range k.Diagnostics() {
		got = append(got, string(x.Code)+" "+strconv.Itoa(x.Line)+
			" "+x.Message)
	}
	return paths, got
}
