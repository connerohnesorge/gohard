package sema

// fx_verify_test.go: regression tests of the v1.1 Stage 3
// verification fixes in the analyzer (#18): more positional generic
// or port actuals than formals are an error (they were dropped
// silently), and T(l to r) as a subtype keeps its index constraint (a
// generic type actual lost it).

import (
	"fmt"
	"strings"
	"testing"
)

// TestFxTooManyPositional: an association list with more positional
// actuals than the unit has formals of the kind is reported once, at
// the first extra actual, for every count and every kind of generic
// map (package, subprogram and protected type instances, a variable's
// map, a component instance) and port maps; up to the count it is
// legal.
func TestFxTooManyPositional(t *testing.T) {
	decls := lines(
		"package gp is",
		"  generic (a : integer; b : integer := 0);",
		"end package;",
		"entity sub is",
		"  generic (a : integer; b : integer := 0);",
		"  port (x : in bit := '0'; y : in bit := '0');",
		"end entity;",
		"architecture a of sub is begin end architecture;",
		"entity e is end entity;",
		"architecture a of e is",
		"  procedure pp generic (a : integer; b : integer := 0)",
		"    parameter (x : integer) is begin end procedure;",
		"  type pt is protected",
		"    generic (a : integer; b : integer := 0);",
		"  end protected;",
		"  type pt is protected body end protected body;",
		"  signal s : bit;",
	)
	for n := 1; n <= 4; n++ {
		acts := make([]string, n)
		for i := range acts {
			acts[i] = fmt.Sprint(i + 1)
		}
		gm := "generic map (" + strings.Join(acts, ", ") + ")"
		sigs := strings.TrimSuffix(strings.Repeat("s, ", n), ", ")
		src := decls + lines(
			"  package ip is new work.gp "+gm+";",
			"  procedure q is new pp "+gm+";",
			"  type ti is new pt "+gm+";",
			"begin",
			"  u : entity work.sub "+gm+" port map ("+sigs+");",
			"  process",
			"    variable v : pt "+gm+";",
			"  begin wait; end process;",
			"end architecture;",
		)
		var want []string
		if n > 2 {
			msg := func(line int, owner, kind string) string {
				return fmt.Sprintf("%d: found at least 3 positional "+
					"actuals but %s has only 2 %ss", line, owner, kind)
			}
			want = []string{msg(18, "gp", "generic"),
				msg(19, "pp", "generic"), msg(20, "pt", "generic"),
				msg(22, "sub", "generic"), msg(22, "sub", "port"),
				msg(24, "pt", "generic")}
		}
		checkExact(t, []wantCase{{name: fmt.Sprint(n), src: src,
			want: want}})
	}
}

// TestFxSliceSubtype: T(l to r) and T(l downto r) as a type mark (the
// parser's slice form) denote the index-constrained subtype of T, as
// T(r) ranges do in a subtype indication.
func TestFxSliceSubtype(t *testing.T) {
	r := analyzeOpen(t, "2019", lines(
		"entity e is end entity;",
		"architecture a of e is",
		"  procedure pp generic (type t) parameter (x : integer) is",
		"  begin end procedure;",
		"  procedure q1 is new pp generic map (t => bit_vector(0 to 2));",
		"  procedure q2 is new pp generic map (t => string(4 downto 1));",
		"  procedure q3 is new pp generic map (t => bit_vector);",
		"begin end architecture;",
	))
	if es := errorLines(r); len(es) > 0 {
		t.Fatal(es)
	}
	want := map[string]bool{"q1": true, "q2": true, "q3": false}
	seen := 0
	for _, u := range r.Units {
		for _, d := range u.Decls {
			cons, ok := want[d.Text]
			if !ok {
				continue
			}
			seen++
			if len(d.Generics) != 1 || d.Generics[0].Actual == nil {
				t.Fatalf("%s: generics %v", d.Text, d.Generics)
			}
			at := d.Generics[0].Actual.Type
			if got := len(at.IndexCons) == 1; got != cons ||
				at.Base().Class != TypeArray {
				t.Errorf("%s: actual %s constrained %v, want %v",
					d.Text, at.Name(), got, cons)
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("found %d of the instances", seen)
	}
}
