package psl

import (
	"fmt"
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

// instanceSrc is builtinSrc's design with the PSL declarations DECLS in
// the architecture's declarative part.
const instanceSrc = `library ieee; use ieee.std_logic_1164.all;
entity s is end;
architecture rtl of s is
  signal clk : std_logic := '0';
  signal a, b, c : std_logic := '0';
  default clock is rising_edge(clk);
  DECLS
begin
  clk <= not clk after 5 ns;
  a <= '1' after 20 ns, '0' after 30 ns;
  b <= '1' after 30 ns, '0' after 40 ns;
  c <= '1' after 50 ns, '0' after 60 ns;
  x: DIR
  stopper: process begin wait for 100 ns; std.env.finish; end process;
end;
`

// TestInstances (#66): property and sequence instances (with formals
// bound to actuals, nested, a sequence actual) and the next_event
// family simulate and fail (or are hit) at the ticks GHDL 6.0.0 gives
// for the rows it runs (named rows; GHDL cannot analyze a property with
// formals, "rewrite_property: cannot handle N_BOOLEAN_PARAMETER", so
// those are hand-checked). a holds at 25 ns, b at 35 ns, c at 55 ns.
func TestInstances(t *testing.T) {
	rows := []struct {
		decls, dir string
		want       []int64
	}{
		// GHDL: 35 ns.
		{"property p_ac is always (a -> next c);", "assert p_ac;",
			[]int64{35}},
		{"property p (boolean l, r) is always (l -> next r);",
			"assert p(a, c);", []int64{35}},
		{"property p (boolean l, r) is always (l -> next r);",
			"assert p(a, b);", nil},
		// GHDL: 45 ns.
		{"sequence s_ab is {a; b};", "assert always ({s_ab} |=> c);",
			[]int64{45}},
		{"sequence s_ab is {a; b};", "assert always (s_ab |=> c);",
			[]int64{45}},
		// GHDL: cover hit at 35 ns.
		{"sequence s (boolean l, r) is {l; r};", "cover {s(a, b)};",
			[]int64{35}},
		{"sequence s (boolean l, r) is {l; r};\n" +
			"  property p (sequence q; boolean r) is always (q |=> r);",
			"assert p({a; b}, c);", []int64{45}},
		{"sequence s (boolean l, r) is {l; r};\n" +
			"  property p (boolean l, r) is always (s(l, r) |=> c);",
			"assert p(a, b);", []int64{45}},
		{"property p (property q) is always (a -> q);",
			"assert p(next c);", []int64{35}},
		{"property p (const n) is always (a -> next[n] c);",
			"assert p(3);", nil},
		// GHDL: 35, 25 and 35 ns.
		{"", "assert always (a -> next_event(b)(c));", []int64{35}},
		{"", "assert always (a -> next_event_a(clk)[1 to 2](b));",
			[]int64{25}},
		{"", "assert always (a -> next_event_e!(clk)[1 to 2](c));",
			[]int64{35}},
		{"", "assert always (a -> next_event(clk)[4](c));", nil},
		{"", "assert always (a -> next_event!(c)[2](b));", []int64{100}},
		// Instances in parentheses, hand-checked against the rows
		// above: 35, 45 and 35 ns.
		{"property p is (a -> next c);", "assert always (p);",
			[]int64{35}},
		{"property p is always (a -> next c);", "assert ((p));",
			[]int64{35}},
		{"sequence s_ab is {a; b};\n  property p_c is next[0] c;",
			"assert always {s_ab} |=> (p_c);", []int64{45}},
		{"sequence s_ab is {a; b};", "cover {(s_ab)};", []int64{35}},
		{"property p (property q) is always (a -> (q));",
			"assert p(next c);", []int64{35}},
		// With actuals the parser reads (p(a, c)) as an HDL call, whose
		// actuals are HDL expressions with no PSLBoolean around them:
		// 35, 35, 35, 35 and 45 ns as the rows above.
		{"property p (boolean l, r) is always (l -> next r);",
			"assert (p(a, c));", []int64{35}},
		{"property p (boolean l) is l -> next c;",
			"assert always (p(a));", []int64{35}},
		{"sequence s (boolean l, r) is {l; r};", "cover {(s(a, b))};",
			[]int64{35}},
		{"property q (boolean l) is l -> next c;\n" +
			"  property p (boolean l) is always ((q(l)));",
			"assert (p(a));", []int64{35}},
		{"sequence s_ab is {a; b};\n" +
			"  property p (sequence q; boolean r) is always (q |=> r);",
			"assert (p(s_ab, c));", []int64{45}},
	}
	for _, r := range rows {
		src := strings.NewReplacer("DECLS", r.decls, "DIR", r.dir).
			Replace(instanceSrc)
		checkTicks(t, r.decls+" "+r.dir, src, r.want)
	}
}

// TestInstanceUnsupported (#66): a formal parameter inside an HDL
// expression, an infinite next_event_a range and forall make the
// directive report unsupported/construct at time 0.
func TestInstanceUnsupported(t *testing.T) {
	cases := []struct{ decls, dir, want string }{
		{"property p (boolean l) is always (l = '1');", "assert p(a);",
			"a formal parameter of a property or sequence inside an " +
				"HDL expression"},
		{"", "assert always (a -> next_event_a(b)[1 to inf](c));",
			"an infinite next_event_a or next_event_e range"},
		{"", "assert forall i in {0 to 1} : always (a -> b);", "forall"},
	}
	for _, c := range cases {
		src := strings.NewReplacer("DECLS", c.decls, "DIR", c.dir).
			Replace(instanceSrc)
		evs, ds := simBuiltin(t, c.dir, src)
		var got []string
		for _, d := range ds {
			got = append(got, string(d.Code)+" "+strconv.Itoa(d.Line)+
				" "+d.Message)
		}
		want := []string{"unsupported/construct 13 " + c.want +
			" in a PSL directive is not supported yet, please report"}
		if !slices.Equal(got, want) || len(evs) != 0 {
			t.Errorf("%s %s: %v, events %v", c.decls, c.dir, got, evs)
		}
	}
}

// compileDirective analyzes src (top s, VHDL-2008) and compiles its
// directive x, also when analysis reports errors (returned).
func compileDirective(t *testing.T, src string) (*directive,
	[]diag.Diagnostic,
) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = "2008"
	r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
		Files: []sema.Source{{Path: "s.vhd", Text: []byte(src)}}})
	var errs []diag.Diagnostic
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	var dir *directive
	stub := func(d *elab.Design, p *elab.Proc) (sim.Code, error) {
		if p.Path == "s.x" && p.Stmt != nil && IsDirective(p.Stmt.Node) {
			dir = compile(d, p)
		}
		return nil, nil
	}
	elab.Elaborate(r, elab.Options{Top: "s", Budget: eval.NewBudget(0),
		Code: stub})
	if dir == nil {
		t.Fatalf("no directive x in %s", src)
	}
	return dir, errs
}

// elabDiags analyzes and elaborates src (top s, VHDL-2008) with the
// PSL runner and returns the elaboration diagnostics as code, line and
// message.
func elabDiags(t *testing.T, src string) []string {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = "2008"
	r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
		Files: []sema.Source{{Path: "s.vhd", Text: []byte(src)}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("analysis: %s %s", d.Code, d.Message)
		}
	}
	_, ds := elab.Elaborate(r, elab.Options{Top: "s",
		Budget: eval.NewBudget(0),
		Code:   Wrap(elab.Interpreter(elab.InterpOptions{}))})
	var out []string
	for _, d := range ds {
		out = append(out, string(d.Code)+" "+strconv.Itoa(d.Line)+" "+
			d.Message)
	}
	return out
}

// TestRecursiveInstanceGuard (#66): sema reports a property that
// instantiates itself; elaborated anyway, its directive stops at the
// first instance inside its own expansion (unsupported/construct)
// instead of expanding it, also when the body instantiates it twice.
func TestRecursiveInstanceGuard(t *testing.T) {
	for _, decl := range []string{
		"property p is always (a -> next p);",
		"property p is (always p) and (always p);",
	} {
		src := strings.NewReplacer("DECLS", decl, "DIR", "assert p;").
			Replace(instanceSrc)
		dir, errs := compileDirective(t, src)
		if len(errs) == 0 {
			t.Errorf("%s: no analysis error", decl)
		}
		if want := "a recursive property or sequence instance in a PSL " +
			"directive"; dir.unsup != want || dir.err != nil {
			t.Errorf("%s: unsup %q, err %v", decl, dir.unsup, dir.err)
		}
	}
}

// doublingDecls declares p0 and n properties pK = (always pK-1) and
// (always pK-1): legal, and pn expands to 2^n copies of p0.
func doublingDecls(n int) string {
	var b strings.Builder
	b.WriteString("property p0 is always (a -> next c);")
	for k := 1; k <= n; k++ {
		fmt.Fprintf(&b, "\n  property p%d is (always p%d) and "+
			"(always p%d);", k, k-1, k-1)
	}
	return b.String()
}

// TestCompileBound (#66, P7): a directive whose instances expand to
// more than maxCompile operators is a run-time error at elaboration,
// before it is built, so 2^21 copies of a property neither take the
// memory nor the time they would; 2^8 copies run.
func TestCompileBound(t *testing.T) {
	over := strings.NewReplacer("DECLS", doublingDecls(21),
		"DIR", "assert p21;").Replace(instanceSrc)
	want := []string{"sim/runtime-error 34 PSL directive x expands to " +
		"more than 65536 operators in s.x at 0 fs"}
	if got := elabDiags(t, over); !slices.Equal(got, want) {
		t.Errorf("2^21 copies: %v, want %v", got, want)
	}
	under := strings.NewReplacer("DECLS", doublingDecls(8),
		"DIR", "assert p8;").Replace(instanceSrc)
	checkTicks(t, "2^8 copies", under, []int64{35})
}

// TestNextEventCountError (#66): a next_event count below 1 and an
// empty or non-positive next_event_a, next_event_e, next_a or next_e
// range whose bounds are not literals (sema reports literal ones) are
// a run-time error at elaboration instead of an always-true or
// always-false property.
func TestNextEventCountError(t *testing.T) {
	cases := []struct{ dir, want string }{
		{"assert always (a -> next_event(b)[K](c));",
			"the next_event count 0 is below 1"},
		{"assert always (a -> next_event!(b)[K](c));",
			"the next_event count 0 is below 1"},
		{"assert always (a -> next_event_a(b)[3 to K + 2](c));",
			"the next_event_a range 3 to 2 is empty"},
		{"assert always (a -> next_event_e(b)[K to 2](c));",
			"the next_event_e range 0 to 2 starts below 1"},
		{"assert always (a -> next_a[3 to K + 2](c));",
			"the next_a range 3 to 2 is empty"},
		{"assert always (a -> next_e![K + 2 to 1](c));",
			"the next_e range 2 to 1 is empty"},
	}
	for _, c := range cases {
		src := strings.NewReplacer("DECLS", "constant K : natural := 0;",
			"DIR", c.dir).Replace(instanceSrc)
		want := []string{"sim/runtime-error 13 " + c.want +
			" in s.x at 0 fs"}
		if got := elabDiags(t, src); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", c.dir, got, want)
		}
	}
	src := strings.NewReplacer("DECLS", "constant K : natural := 1;",
		"DIR", "assert always (a -> next_event(b)[K](c));").
		Replace(instanceSrc)
	checkTicks(t, "next_event(b)[K] with K = 1", src, []int64{35})
}

// TestNamedAntecedent (#66): an assert is covered (PslIsAssertCovered)
// once the antecedent of its top-level implication matches, and a
// property instance has its body's antecedent: each named form below
// compiles to the antecedent of the inline assert always (a -> next
// c), not to none (covered at the first tick).
func TestNamedAntecedent(t *testing.T) {
	ante := func(decls, dir string) string {
		src := strings.NewReplacer("DECLS", decls, "DIR", dir).
			Replace(instanceSrc)
		d, errs := compileDirective(t, src)
		if len(errs) > 0 {
			t.Fatalf("%s %s: %v", decls, dir, errs)
		}
		if d.ante == nil {
			return "none"
		}
		return fmt.Sprintf("%s all=%v", d.ante.key, d.anteAll)
	}
	want := ante("", "assert always (a -> next c);")
	if want == "none" {
		t.Fatal("inline assert: no antecedent")
	}
	for _, r := range []struct{ decls, dir string }{
		{"property p is always (a -> next c);", "assert p;"},
		{"property p is always (a -> next c);", "assert (p);"},
		{"property p is (a -> next c);", "assert always p;"},
		{"property p (boolean l, r) is always (l -> next r);",
			"assert p(a, c);"},
		{"property p (boolean l, r) is always (l -> next r);",
			"assert (p(a, c));"},
		{"property p (boolean l) is l -> next c;",
			"assert always (p(a));"},
		{"property q is always (a -> next c);\n  property p is q;",
			"assert p;"},
		{"property p (property q) is q;",
			"assert p(always (a -> next c));"},
	} {
		if got := ante(r.decls, r.dir); got != want {
			t.Errorf("%s %s: antecedent %s, want %s", r.decls, r.dir,
				got, want)
		}
	}
}
