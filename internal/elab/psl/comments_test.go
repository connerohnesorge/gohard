package psl

import (
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
		cfg := config.Defaults()
		cfg.Standard = "2008"
		cfg.PSL.Comments = true
		r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
			Files: []sema.Source{{Path: "t.vhd", Text: []byte(tc.src)}}})
		for _, d := range r.Diagnostics {
			if d.Severity == diag.Error {
				t.Fatalf("%s: analysis: %s %s", tc.name, d.Code, d.Message)
			}
		}
		b := eval.NewBudget(0)
		d, ds := elab.Elaborate(r, elab.Options{Top: "t", Budget: b,
			Code: Wrap(elab.Interpreter(elab.InterpOptions{}))})
		if d == nil || len(ds) > 0 || len(d.Missing) > 0 {
			t.Fatalf("%s: elaborate: %v missing %v", tc.name, ds,
				d.Missing)
		}
		var paths []string
		for _, p := range d.Procs {
			paths = append(paths, p.Path)
		}
		wantPaths := []string{"t._p0", "t._p1", "t._p2", "t.x",
			"t.stopper", "t._p3"}
		if !slices.Equal(paths, wantPaths) {
			t.Errorf("%s: processes %v, want %v", tc.name, paths,
				wantPaths)
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
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: diagnostics:\n%s", tc.name,
				strings.Join(got, "\n"))
		}
	}
}
