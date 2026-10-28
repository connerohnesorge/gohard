package sema

// l_protgen_test.go checks the analysis of the generic clauses on
// protected types of v1.1 Stage 3 (#18, VHDL-2019 LRM 5.6.2, 5.6.4,
// 6.4.2.4): protected type instantiations, generic maps on variable
// declarations and in a protected type's header, with the exact
// diagnostics of their illegal forms, and the model they build.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// lPkg declares, on lines 1 to 17, the generic protected type gp
// (step has a default, size and elem have none), the plain protected
// type pt, and their bodies.
var lPkg = lines(
	"package pk is",
	"  type gp is protected",
	"    generic (step : integer := 1; size : integer; type elem);",
	"    procedure put(x : elem);",
	"  end protected;",
	"  type pt is protected",
	"    procedure inc;",
	"  end protected;",
	"end package;",
	"package body pk is",
	"  type gp is protected body",
	"    procedure put(x : elem) is begin end procedure;",
	"  end protected body;",
	"  type pt is protected body",
	"    procedure inc is begin end procedure;",
	"  end protected body;",
) + "end package body;\n"

// lArch wraps declarative items (from line 21) and process
// declarations in an architecture using pk.
func lArch(decls, procDecls string) string {
	return lPkg + lines(
		"use work.pk.all;",
		"entity e is end entity;",
		"architecture a of e is",
	) + decls + "begin\n  process\n" + procDecls +
		"  begin\n    wait;\n  end process;\nend architecture;\n"
}

func TestLProtGenericRules(t *testing.T) {
	checkExact(t, []wantCase{
		{name: "legal", src: lArch(lines(
			"  type g3 is new gp generic map (step => 3, size => 2,",
			"    elem => boolean);",
			"  type g4 is new gp generic map (4, 8, bit);",
			"  type fx is protected",
			"    generic (k : integer);",
			"    generic map (k => 7);",
			"    impure function get return integer;",
			"  end protected;",
			"  type fx is protected body",
			"    impure function get return integer is",
			"    begin return k; end function;",
			"  end protected body;",
			"  shared variable s3 : g3;",
			"  shared variable sv : gp generic map (size => 1,",
			"    elem => integer);",
			"  type g3_arr is array (0 to 1) of g3;",
			"  type g3_ptr is access g3;",
			"  type lg is protected",
			"    generic (k : integer);",
			"  end protected;",
			"  type lg is protected body",
			"  end protected body;",
			"  type lgi is new lg generic map (k => 1);",
			"  shared variable sl : lgi;",
		), lines(
			"    variable v : gp generic map (size => 4, elem => bit);",
			"    variable w : g4;",
			"    variable f : fx;",
			"    variable arr : g3_arr;",
			"    variable p : g3_ptr := new g3;",
		))},
		{name: "instance before the body", src: lArch(lines(
			"  type lg is protected",
			"    generic (k : integer);",
			"  end protected;",
			"  type lgi is new lg generic map (k => 1);",
			"  shared variable sl : lgi;",
			"  type lg is protected body",
			"  end protected body;",
		), ""), want: []string{
			"25: shared variable of protected type lg declared before " +
				"the body of lg is elaborated",
		}},
		{name: "map on a non-generic type", src: lArch("", lines(
			"    variable v : integer generic map (k => 1);",
			"    variable w : pt generic map (k => 1);",
		)), want: []string{
			"23: integer is not a generic protected type: a variable " +
				"of it cannot have a generic map (LRM 6.4.2.4)",
			"24: pt is not a generic protected type: a variable of it " +
				"cannot have a generic map (LRM 6.4.2.4)",
		}},
		{name: "map on an instance", src: lArch(lines(
			"  type g3 is new gp generic map (size => 2, elem => bit);",
		), lines(
			"    variable v : g3 generic map (step => 1);",
		)), want: []string{
			"24: g3 is not a generic protected type: a variable of it " +
				"cannot have a generic map (LRM 6.4.2.4)",
		}},
		{name: "no map and no defaults", src: lArch(lines(
			"  shared variable s : gp;",
		), lines(
			"    variable v : gp;",
		)), want: []string{
			"21: generic elem of gp has no default and no actual",
			"21: generic size of gp has no default and no actual",
			"24: generic elem of gp has no default and no actual",
			"24: generic size of gp has no default and no actual",
		}},
		{name: "incomplete maps", src: lArch(lines(
			"  type g1 is new gp generic map (elem => bit);",
			"  type g2 is new gp;",
		), lines(
			"    variable v : gp generic map (size => 2);",
		)), want: []string{
			"21: generic size of gp has no default and no actual",
			"22: generic elem of gp has no default and no actual",
			"22: generic size of gp has no default and no actual",
			"25: generic elem of gp has no default and no actual",
		}},
		{name: "unknown formal", src: lArch(lines(
			"  type g1 is new gp generic map (size => 1, elem => bit,",
			"    zz => 3);",
		), lines(
			"    variable v : gp generic map (size => 2, elem => bit,",
			"      qq => 1);",
		)), want: []string{
			"22: zz is not a generic of gp",
			"26: qq is not a generic of gp",
		}},
		{name: "instance of a non-generic type", src: lArch(lines(
			"  type i1 is new pt;",
			"  type i2 is new integer generic map (k => 1);",
			"  type g3 is new gp generic map (size => 2, elem => bit);",
			"  type i3 is new g3;",
		), ""), want: []string{
			"21: pt is not a generic protected type (LRM 5.6.4)",
			"22: integer is not a generic protected type (LRM 5.6.4)",
			"24: g3 is not a generic protected type (LRM 5.6.4)",
		}},
		{name: "header map", src: lArch(lines(
			"  type hx is protected",
			"    generic (k : integer; j : integer);",
			"    generic map (k => 7);",
			"  end protected;",
			"  type hx is protected body",
			"  end protected body;",
		), lines(
			"    variable v : hx generic map (k => 1, j => 2);",
		)), want: []string{
			"23: generic j of hx has no default and no actual",
			"29: hx is not a generic protected type: a variable of it " +
				"cannot have a generic map (LRM 6.4.2.4)",
		}},
	})
}

// TestLProtGenericModel: an instantiated type is a protected type of
// its own, sharing its generic type's members, whose Inst names the
// generic type and holds the map; a variable with a generic map is of
// an anonymous subtype of the generic type carrying the map; a
// variable of an instance carries its type's map (ProtectedInst).
func TestLProtGenericModel(t *testing.T) {
	r := analyzeOpen(t, "2019", lArch(lines(
		"  type g3 is new gp generic map (step => 3, size => 2,",
		"    elem => boolean);",
		"  subtype s3 is g3;",
	), lines(
		"    variable v : gp generic map (size => 4, elem => bit);",
		"    variable w : s3;",
		"    variable u : pt;",
	)))
	if es := errorLines(r); len(es) > 0 {
		t.Fatal(es)
	}
	decls := map[string]*Decl{}
	for _, u := range r.Units {
		for _, d := range u.Decls {
			decls[d.Text] = d
		}
	}
	gp, g3, v, w, u := decls["gp"], decls["g3"], decls["v"], decls["w"],
		decls["u"]
	if gp == nil || g3 == nil || v == nil || w == nil || u == nil {
		t.Fatalf("missing declarations: %v", decls)
	}
	if !GenericProtected(gp.Type) || GenericProtected(g3.Type) ||
		GenericProtected(decls["pt"].Type) {
		t.Errorf("GenericProtected: gp %v g3 %v pt %v",
			GenericProtected(gp.Type), GenericProtected(g3.Type),
			GenericProtected(decls["pt"].Type))
	}
	b := g3.Type
	if b.Class != TypeProtected || b.Base() != b || b.Inst == nil ||
		b.Inst.Of != gp || b.Members != gp.Type.Members ||
		len(b.Inst.Map) != 3 || b.Inst.Map[0].Formal.Text != "step" {
		t.Errorf("g3: %+v", b)
	}
	if v.Type.Base() != gp.Type || v.Type.Inst == nil ||
		v.Type.Inst.Of != gp || len(v.Type.Inst.Map) != 2 ||
		v.Type.Name() != "gp" {
		t.Errorf("v: %+v", v.Type)
	}
	if w.Type.Base() != g3.Type || w.Type.ProtectedInst() != b.Inst {
		t.Errorf("w: %+v", w.Type)
	}
	if u.Type.ProtectedInst() != nil {
		t.Errorf("u: %+v", u.Type)
	}
}

// TestLProtGenericMissingProp is a property: for random generic maps
// of a type with a random mix of generics with and without defaults,
// the errors are exactly one "has no default and no actual" per
// generic that has no default and no association, in an instantiated
// type's map and a variable's alike.
func TestLProtGenericMissingProp(t *testing.T) {
	for seed := range uint64(40) {
		r := &env.SplitMix64{State: seed}
		n := env.Intn(r, 4) + 1
		var gens, assoc []string
		var missing []string
		for i := range n {
			def, given := env.Intn(r, 2) == 0, env.Intn(r, 2) == 0
			g := fmt.Sprintf("g%d : integer", i)
			if def {
				g += " := 1"
			}
			gens = append(gens, g)
			if given {
				assoc = append(assoc, fmt.Sprintf("g%d => %d", i, i))
			} else if !def {
				missing = append(missing, fmt.Sprintf("g%d", i))
			}
		}
		gm := ""
		if len(assoc) > 0 {
			gm = " generic map (" + strings.Join(assoc, ", ") + ")"
		}
		src := lines(
			"entity e is end entity;",
			"architecture a of e is",
			"  type q is protected",
			"    generic ("+strings.Join(gens, "; ")+");",
			"  end protected;",
			"  type q is protected body",
			"  end protected body;",
			"  type qi is new q"+gm+";",
			"begin",
			"  process",
			"    variable v : q"+gm+";",
			"  begin",
			"    wait;",
			"  end process;",
			"end architecture;",
		)
		var want []string
		for _, line := range []int{8, 11} {
			for _, g := range missing {
				want = append(want, fmt.Sprintf("%d: generic %s of q "+
					"has no default and no actual", line, g))
			}
		}
		got := errorLines(analyzeOpen(t, "2019", src))
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("seed %d:\n%s\ngot:\n%s\nwant:\n%s", seed, src,
				strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}
