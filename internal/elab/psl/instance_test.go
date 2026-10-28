package psl

import (
	"slices"
	"strconv"
	"strings"
	"testing"
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
	}
	for _, r := range rows {
		src := strings.NewReplacer("DECLS", r.decls, "DIR", r.dir).
			Replace(instanceSrc)
		checkTicks(t, r.decls+" "+r.dir, src, r.want)
	}
}

// TestInstanceUnsupported (#66): a formal parameter inside an HDL
// expression, a recursive property, an infinite next_event_a range and
// forall make the directive report unsupported/construct at time 0.
func TestInstanceUnsupported(t *testing.T) {
	cases := []struct{ decls, dir, want string }{
		{"property p (boolean l) is always (l = '1');", "assert p(a);",
			"a formal parameter of a property or sequence inside an " +
				"HDL expression"},
		{"property p is always (a -> next p);", "assert p;",
			"a recursive property or sequence instance"},
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
