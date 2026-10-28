package lint

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

// latchy is an architecture of entity name whose combinational process
// assigns n signals, each under an if without else: n latches.
func latchy(name string, n int) string {
	var b strings.Builder
	b.WriteString(header + "entity " + name + " is\n" +
		"  port (en, a : in std_logic);\nend;\n" +
		"architecture a of " + name + " is\n")
	for i := range n {
		b.WriteString("  signal s" + strconv.Itoa(i) + " : std_logic;\n")
	}
	b.WriteString("begin\n  p : process (en, a) begin\n")
	for i := range n {
		b.WriteString("    if en = '1' then s" + strconv.Itoa(i) +
			" <= a; end if;\n")
	}
	b.WriteString("  end process;\nend;\n")
	return b.String()
}

// unitOf returns the unit of res named name of kind k.
func unitOf(res *sema.Result, k sema.UnitKind, name string) *sema.Unit {
	for _, u := range res.Units {
		if u.Kind == k && (u.Name == sema.Key(name) ||
			k == sema.UnitArchitecture && u.Of == sema.Key(name)) {
			return u
		}
	}
	return nil
}

// TestBudget: lint runs under a step budget per unit (P7). A unit that
// exceeds it gets one sema/eval-limit warning at its name in place of
// its findings, the other units keep theirs, and the work done past
// the limit is bounded.
func TestBudget(t *testing.T) {
	r := analyze(t, map[string]string{
		"big.vhd":   latchy("big", 400),
		"small.vhd": latchy("small", 1),
	})
	big := unitOf(r, sema.UnitArchitecture, "big")
	small := unitOf(r, sema.UnitArchitecture, "small")
	m := newMeter(eval.EditorSteps)
	all := check(r, allOn, m)
	used, usedSmall := m.used[big], m.used[small]
	// 400 latches, 400 signals never read; small has one of each.
	if len(all) != 802 || used <= 10*usedSmall {
		t.Fatalf("%d findings, big %d steps, small %d steps", len(all),
			used, usedSmall)
	}

	limit := used / 2
	m = newMeter(limit)
	got := check(r, allOn, m)
	var warn []string
	for _, d := range got {
		switch {
		case d.Code == "sema/eval-limit":
			warn = append(warn, show(d))
		case d.Path == "big.vhd":
			t.Errorf("finding of the unit over budget: %s", show(d))
		}
	}
	want := "big.vhd:5:14: sema/eval-limit: the lint rules in " +
		"architecture a could not be computed within 1,000,000 " +
		"evaluation steps"
	if !slices.Equal(warn, []string{want}) {
		t.Errorf("warnings %q, want %q", warn, want)
	}
	if len(got) != 3 {
		t.Errorf("want small's 2 findings and the warning: %q", shows(got))
	}
	// Past the limit each rule stops at its next step check: the
	// overrun is a small part of the unit's full cost.
	if m.used[big] > limit+used/10 {
		t.Errorf("big used %d steps under a limit of %d (full %d)",
			m.used[big], limit, used)
	}
}

// TestStepsLinear: a unit's lint steps grow in proportion to its size;
// no rule rescans the unit for each statement.
func TestStepsLinear(t *testing.T) {
	var used [2]int64
	for i, n := range []int{200, 400} {
		r := analyze(t, map[string]string{"a.vhd": latchy("e", n)})
		m := newMeter(eval.EditorSteps)
		check(r, allOn, m)
		used[i] = m.used[unitOf(r, sema.UnitArchitecture, "e")]
	}
	if used[1] > used[0]*21/10 {
		t.Fatalf("200 assignments %d steps, 400 assignments %d steps",
			used[0], used[1])
	}
}

// TestDeterministic: lint output is a function of the design (P1):
// the same for every order of the input files and for every run.
func TestDeterministic(t *testing.T) {
	dir := "../../testdata/cli"
	var srcs []sema.Source
	for _, c := range []string{
		"check-lint-unused-signal/in/lint.vhd",
		"check-lint-sensitivity-list/in/sens.vhd",
		"check-lint-latch/in/latch.vhd",
		"check-lint-clock-edge-style/in/edges.vhd",
	} {
		b, err := os.ReadFile(filepath.Join(dir, c))
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, sema.Source{Path: filepath.Base(c), Text: b})
	}
	srcs = append(srcs, sema.Source{Path: "more.vhd",
		Text: []byte(latchy("more", 3))})
	var want []string
	for seed := range uint64(24) {
		order := env.Shuffle(seed, 17, len(srcs))
		in := make([]sema.Source, len(srcs))
		for i, j := range order {
			in[i] = srcs[j]
		}
		r := sema.Analyze(sema.Input{Files: in,
			Config: config.Defaults()})
		for range 2 {
			var got []string
			for _, d := range Check(r, allOn) {
				got = append(got, show(d))
			}
			if want == nil {
				want = got
				if len(want) < 10 {
					t.Fatalf("too few findings to compare: %q", want)
				}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("order %v:\n%q\nwant\n%q", order, got, want)
			}
		}
	}
}

// edgy is an architecture of entity name with n concurrent signal
// assignments and one clocked process using rising_edge, first or
// last.
func edgy(name string, n int, first bool) string {
	clocked := "  c : process (clk) begin\n" +
		"    if rising_edge(clk) then q <= a; end if;\n" +
		"  end process;\n"
	var b strings.Builder
	b.WriteString(header + "entity " + name + " is\n" +
		"  port (clk, a : in std_logic; q : out std_logic);\nend;\n" +
		"architecture a of " + name + " is\n")
	b.WriteString("  signal s : std_logic_vector(0 to " +
		strconv.Itoa(n-1) + ");\nbegin\n")
	if first {
		b.WriteString(clocked)
	}
	for i := range n {
		b.WriteString("  s(" + strconv.Itoa(i) + ") <= a;\n")
	}
	if !first {
		b.WriteString(clocked)
	}
	b.WriteString("end;\n")
	return b.String()
}

// tick is a design whose only edge test is clk'event.
const tick = header + `entity small is
  port (clk, a : in std_logic; q : out std_logic);
end;
architecture a of small is
begin
  p : process (clk) begin
    if clk'event and clk = '1' then q <= a; end if;
  end process;
end;
`

// TestEdgeBudget: lint/clock-edge-style depends on the whole design.
// When the unit holding the design's only rising_edge runs out of
// budget before the scan reaches it, the other units' 'event tests
// get a sema/eval-limit warning for the rule, not silence; when the
// scan reached a rising_edge first, they keep their findings.
func TestEdgeBudget(t *testing.T) {
	cfg := config.Lint{ClockEdgeStyle: "warning"}
	finding := "small.vhd:8:12: lint/clock-edge-style: this edge test " +
		"uses 'event; the design also uses rising_edge or falling_edge"
	bigLimit := "big.vhd:5:14: sema/eval-limit: the lint rules in " +
		"architecture a could not be computed within 1,000,000 " +
		"evaluation steps"
	smallLimit := "small.vhd:5:14: sema/eval-limit: lint/clock-edge-" +
		"style in architecture a could not be computed within " +
		"1,000,000 evaluation steps"
	for _, c := range []struct {
		first bool
		want  []string
	}{
		{first: false, want: []string{bigLimit, smallLimit}},
		{first: true, want: []string{bigLimit, finding}},
	} {
		r := analyze(t, map[string]string{
			"big.vhd":   edgy("big", 300, c.first),
			"small.vhd": tick,
		})
		big := unitOf(r, sema.UnitArchitecture, "big")
		m := newMeter(eval.EditorSteps)
		if got := shows(check(r, cfg, m)); !slices.Equal(got,
			[]string{finding}) {
			t.Fatalf("first %v, full budget: %q", c.first, got)
		}
		got := shows(check(r, cfg, newMeter(m.used[big]/2)))
		if !slices.Equal(got, c.want) {
			t.Errorf("first %v, half budget:\n%q\nwant\n%q", c.first,
				got, c.want)
		}
	}
}
