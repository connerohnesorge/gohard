package coverage_test

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/corpus"
	"github.com/connerohnesorge/gohard/v2/internal/coverage"
	"github.com/connerohnesorge/gohard/v2/internal/simrun"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// formsWant is the coverage of testdata/forms.vhd, worked out by hand
// from the source: the loop runs i = 0..3, classify gives v = 0, 1, 1,
// 2, the iteration i = 1 skips the bump and the wait (next), so s has
// 3 events and t 3 (-1, then 1 at 1 ns, where the assignments of i = 1
// and i = 2 meet in one delta, then 2).
//
//   - bump (impure, run by the interpreter): 3 calls.
//   - sq (pure, run by the evaluator): 4 calls with x = -1, 0, 1, 2.
//   - classify: x > 2 once, x > 0 twice, else once.
//   - check (concurrent call on t): initialization and 3 events of t.
//   - w := 1 when v = 1 else 2 when v = 2: v = 1 twice, v = 2 once,
//     no branch once (v = 0).
//   - the selected and conditional assignments: v = 0 once, others 3
//     times; v > 0 three times, else once; v = 2 once, others 3
//     times.
//   - the concurrent assertion on s: initialization and 3 events.
var formsWant = lines(
	"TN:", "SF:forms.vhd",
	"BRDA:15,0,0,1", "BRDA:15,0,1,3",
	"BRDA:23,0,0,1", "BRDA:23,0,1,2", "BRDA:23,0,2,1",
	"BRDA:43,0,0,2", "BRDA:43,0,1,1", "BRDA:43,0,2,1",
	"BRDA:44,0,0,1", "BRDA:44,0,1,3",
	"BRDA:45,0,0,3", "BRDA:45,0,1,1",
	"BRDA:46,0,0,1", "BRDA:46,0,1,3",
	"BRF:14", "BRH:14",
	"DA:10,3", "DA:15,4", "DA:16,1", "DA:18,3",
	"DA:23,4", "DA:24,1", "DA:26,2", "DA:28,1", "DA:34,4",
	"DA:40,1", "DA:41,4", "DA:42,4", "DA:43,4", "DA:44,4", "DA:45,4",
	"DA:46,4", "DA:47,4", "DA:48,3", "DA:49,3", "DA:51,1",
	"DA:54,4", "DA:55,4",
	"LF:22", "LH:22", "end_of_record")

func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

func formsRun(t *testing.T) (corpus.Run, map[string][]byte) {
	t.Helper()
	b, err := os.ReadFile("testdata/forms.vhd")
	if err != nil {
		t.Fatal(err)
	}
	r := corpus.Run{Design: corpus.Design{Name: "forms",
		Testbench: "forms"}, Standard: "2008",
		Paths: []string{"forms.vhd"}}
	return r, map[string][]byte{"forms.vhd": b}
}

// TestFormsCoverage: every statement form the interpreter and the
// evaluator run is counted as the source says, and the coverage
// changes no trace byte, message or step at any seed.
func TestFormsCoverage(t *testing.T) {
	r, src := formsRun(t)
	for seed := range uint64(4) {
		off, err := corpus.Execute(r, src, corpus.Options{Seed: seed,
			Code: simrun.DefaultCode})
		if err != nil {
			t.Fatal(err)
		}
		on, err := corpus.Execute(r, src, corpus.Options{Seed: seed,
			Code: simrun.DefaultCode, Coverage: true})
		if err != nil {
			t.Fatal(err)
		}
		if !on.Simulated || on.Coverage == nil {
			t.Fatalf("seed %d: not simulated: %v", seed, on.Messages)
		}
		if n, a, b, ok := trace.FirstDiff(off.Text, on.Text); ok {
			t.Fatalf("seed %d: trace differs at line %d: %q, %q", seed,
				n, a, b)
		}
		if off.Steps != on.Steps || off.Exit != on.Exit ||
			len(off.Messages) != len(on.Messages) {
			t.Fatalf("seed %d: steps %d/%d exit %d/%d messages %v/%v",
				seed, off.Steps, on.Steps, off.Exit, on.Exit,
				off.Messages, on.Messages)
		}
		if on.Unmatched != 0 {
			t.Errorf("seed %d: %d unmatched", seed, on.Unmatched)
		}
		if got := string(coverage.Encode(*on.Coverage)); got !=
			formsWant {
			t.Fatalf("seed %d: coverage\n%s\nwant\n%s", seed, got,
				formsWant)
		}
	}
}

// TestCoverageReruns: two runs of the same design give the same bytes
// (P1).
func TestCoverageReruns(t *testing.T) {
	r, src := formsRun(t)
	var first []byte
	for i := range 3 {
		res, err := corpus.Execute(r, src, corpus.Options{
			Code: simrun.DefaultCode, Coverage: true})
		if err != nil || res.Coverage == nil {
			t.Fatalf("run %d: %v", i, err)
		}
		b := coverage.Encode(*res.Coverage)
		if first == nil {
			first = b
		} else if string(b) != string(first) {
			t.Fatalf("run %d differs:\n%s\n---\n%s", i, first, b)
		}
	}
	tot := mustParse(t, first).Totals()
	if got := strconv.Itoa(tot.LinesHit) + "/" + strconv.Itoa(tot.Lines); got !=
		"22/22" {
		t.Errorf("lines %s", got)
	}
}

func mustParse(t *testing.T, b []byte) coverage.Report {
	t.Helper()
	r, err := coverage.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestNestedBodies: the statements of subprograms declared in a
// process, in a subprogram and in a protected type body are coverable
// (Units and New find them in Unit.Decls) and counted.
func TestNestedBodies(t *testing.T) {
	src := lines(
		"entity nest is",
		"end entity nest;",
		"architecture sim of nest is",
		"  type counter is protected",
		"    procedure inc;",
		"    impure function get return integer;",
		"  end protected counter;",
		"  type counter is protected body",
		"    variable n : integer := 0;",
		"    procedure inc is",
		"    begin",
		"      n := n + 1;",
		"    end procedure inc;",
		"    impure function get return integer is",
		"    begin",
		"      return n;",
		"    end function get;",
		"  end protected body counter;",
		"  shared variable c : counter;",
		"begin",
		"  p : process",
		"    function twice (x : integer) return integer is",
		"      function one (y : integer) return integer is",
		"      begin",
		"        return y;",
		"      end function one;",
		"    begin",
		"      return one(x) + one(x);",
		"    end function twice;",
		"  begin",
		"    c.inc;",
		"    assert twice(c.get) = 2 report \"twice\";",
		"    wait;",
		"  end process p;",
		"end architecture sim;")
	r := corpus.Run{Design: corpus.Design{Name: "nest",
		Testbench: "nest"}, Standard: "2008", Paths: []string{"nest.vhd"}}
	res, err := corpus.Execute(r, map[string][]byte{"nest.vhd": []byte(src)},
		corpus.Options{Code: simrun.DefaultCode, Coverage: true})
	if err != nil || res.Coverage == nil {
		t.Fatalf("%v %v", err, res.Messages)
	}
	if res.Unmatched != 0 || len(res.Messages) != 0 {
		t.Errorf("unmatched %d, messages %v", res.Unmatched, res.Messages)
	}
	want := lines("TN:", "SF:nest.vhd", "DA:12,1", "DA:16,1", "DA:25,2",
		"DA:28,1", "DA:31,1", "DA:32,1", "DA:33,1", "LF:7", "LH:7",
		"end_of_record")
	if got := string(coverage.Encode(*res.Coverage)); got != want {
		t.Errorf("coverage\n%s\nwant\n%s", got, want)
	}
}

// TestSameLineCovers: two PSL cover directives on one line each count
// their own cover-hit events, so the line counts the larger of the two
// (c1 hits at the 4 rising edges where a is high, c2 at the one where
// b is), not their sum.
func TestSameLineCovers(t *testing.T) {
	src := lines(
		"library ieee;",
		"use ieee.std_logic_1164.all;",
		"use std.env.all;",
		"entity two is",
		"end entity two;",
		"architecture sim of two is",
		"  signal clk : std_logic := '0';",
		"  signal a, b : std_logic := '0';",
		"begin",
		"  clk <= not clk after 5 ns;",
		"  stim : process",
		"  begin",
		"    a <= '1';",
		"    wait for 40 ns;",
		"    a <= '0';",
		"    b <= '1';",
		"    wait for 10 ns;",
		"    b <= '0';",
		"    wait for 20 ns;",
		"    finish;",
		"  end process stim;",
		"  c1 : cover {a = '1'} @ rising_edge(clk); "+
			"c2 : cover {b = '1'} @ rising_edge(clk);",
		"end architecture sim;")
	r := corpus.Run{Design: corpus.Design{Name: "two", Testbench: "two"},
		Standard: "2008", Paths: []string{"two.vhd"}}
	res, err := corpus.Execute(r, map[string][]byte{"two.vhd": []byte(src)},
		corpus.Options{Code: simrun.DefaultCode, Coverage: true})
	if err != nil || res.Coverage == nil {
		t.Fatalf("%v %v", err, res.Messages)
	}
	var msgs []string
	for _, m := range res.Messages {
		msgs = append(msgs, m.Message)
	}
	if !slices.Contains(msgs, "cover c1 hit 4 times") ||
		!slices.Contains(msgs, "cover c2 hit 1 times") {
		t.Fatalf("messages %v", msgs)
	}
	got := string(coverage.Encode(*res.Coverage))
	if !strings.Contains(got, "\nDA:22,4\n") {
		t.Errorf("coverage\n%s\nwant DA:22,4", got)
	}
}
