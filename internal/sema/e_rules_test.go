package sema

// e_rules_test.go checks the v1.1 decl and statement rules of #18
// (Stage 1, builder E): conditional return, inferred constraints,
// variable ports, composites of protected types, protected and access
// formals and private variables. Each rule has its legal and illegal
// form with the exact diagnostic.

import (
	"strings"
	"testing"
)

// ePkg declares a protected type pt with composites of it and a
// pointer to it, as lines 1 to 15 of every protected case.
var ePkg = lines(
	"package pk is",
	"  type pt is protected",
	"    procedure inc;",
	"    impure function get return integer;",
	"  end protected;",
	"  type pt_arr is array (0 to 1) of pt;",
	"  type pt_rec is record a : pt; n : integer; end record;",
	"  type pt_ptr is access pt;",
	"end package;",
	"package body pk is",
	"  type pt is protected body variable n : integer := 0;",
	"    procedure inc is begin n := n + 1; end procedure;",
	"    impure function get return integer is begin return n; end;",
	"  end protected body;",
	"end package body;",
)

func TestECondReturn(t *testing.T) {
	checkExact(t, []wantCase{
		{name: "legal", src: lines(
			"package p is",
			"  function clip(x : integer) return integer;",
			"  procedure q(x : integer);",
			"end package;",
			"package body p is",
			"  function clip(x : integer) return integer is",
			"  begin",
			"    return 0 when x < 0;",
			"    return 1 when x = 0 else unaffected when x > 9;",
			"    return x;",
			"  end function;",
			"  procedure q(x : integer) is",
			"  begin",
			"    return when x < 0;",
			"  end procedure;",
			"end package body;",
		)},
		{name: "function without value", src: lines(
			"package p is",
			"  function f(x : integer) return integer;",
			"end package;",
			"package body p is",
			"  function f(x : integer) return integer is",
			"  begin",
			"    return when x < 0;",
			"    return 1 when x = 0 else when x = 1;",
			"    return x;",
			"  end function;",
			"end package body;",
		), want: []string{
			"7: a return statement in a function needs a value",
			"8: a return statement in a function needs a value",
		}},
		{name: "procedure with value", src: lines(
			"package p is",
			"  procedure q(x : integer);",
			"end package;",
			"package body p is",
			"  procedure q(x : integer) is",
			"  begin",
			"    return 1 when x < 0;",
			"    return unaffected when x < 0;",
			"    return when x < 0 else when x > 0;",
			"  end procedure;",
			"end package body;",
		), want: []string{
			"7: a return statement in a procedure has no value",
			"8: a return statement in a procedure has no value",
			"9: a conditional return in a procedure has one " +
				"condition and no else",
		}},
		{name: "outside a subprogram", src: lines(
			"entity e is end entity;",
			"architecture a of e is",
			"begin",
			"  process begin",
			"    return when true;",
			"    wait;",
			"  end process;",
			"end architecture;",
		), want: []string{
			"5: a return statement must be inside a subprogram",
		}},
	})
}

// TestECondReturnTree checks the statement tree: one branch per
// alternative, values typed against the result, unaffected marked and
// told apart from the procedure form.
func TestECondReturnTree(t *testing.T) {
	src := lines(
		"package p is",
		"  function f(x : integer) return integer;",
		"  procedure q(x : integer);",
		"end package;",
		"package body p is",
		"  function f(x : integer) return integer is",
		"  begin",
		"    return unaffected when x < 0 else 7 when x = 0 else x;",
		"  end function;",
		"  procedure q(x : integer) is",
		"  begin",
		"    return when x < 0;",
		"  end procedure;",
		"end package body;",
	)
	r := analyzeOpen(t, "2019", src)
	if es := errorLines(r); len(es) != 0 {
		t.Fatalf("errors: %v", es)
	}
	var got []string
	for _, u := range r.Units {
		for _, d := range u.Decls {
			if d.Kind != DeclFunction && d.Kind != DeclProcedure ||
				d.Spec == nil {
				continue
			}
			for _, st := range d.Seq {
				if st.Kind != SeqCondReturn {
					continue
				}
				for _, b := range st.Branches {
					got = append(got, condBranchText(b))
				}
			}
		}
	}
	want := "unaffected/cond, value universal_integer/cond, " +
		"value integer/else, none/cond"
	if g := strings.Join(got, ", "); g != want {
		t.Errorf("branches %s, want %s", g, want)
	}
}

func condBranchText(b SeqBranch) string {
	v := "none"
	switch {
	case b.Unaffected:
		v = "unaffected"
	case b.Value != nil:
		v = "value " + strings.ToLower(b.Value.Type.Base().Name())
	}
	c := "else"
	if b.Cond != nil {
		c = "cond"
	}
	return v + "/" + c
}

func TestEInferred(t *testing.T) {
	src := lines(
		"entity e is end entity;",
		"architecture a of e is",
		"  type rec is record f : bit_vector; end record;",
		"  constant k : bit_vector(1 to 5) := \"10110\";",
		"  signal s : bit_vector := k;",
		"  signal u : bit_vector := k & k;",
		"begin",
		"  process",
		"    variable v : bit_vector := k;",
		"    variable w : bit_vector := \"10\";",
		"    variable r : rec := (f => k);",
		"    variable z : bit_vector(1 to 5);",
		"  begin",
		"    z := v;",
		"    w := (others => '0');",
		"    wait;",
		"  end process;",
		"end architecture;",
	)
	r := analyzeOpen(t, "2019", src)
	if es := errorLines(r); len(es) != 0 {
		t.Fatalf("errors: %v", es)
	}
	types := map[string]*Type{}
	for _, u := range r.Units {
		for _, d := range u.Decls {
			if d.Kind.IsObject() {
				types[string(d.Name)] = d.Type
			}
		}
	}
	// A value with a subtype known during analysis gives its
	// constraint; any other value one known at run time.
	for _, n := range []string{"s", "v"} {
		x := types[n]
		if x.isDynamic() || notFullyConstrained(x, 0) ||
			x.Constraints()[0] != types["k"].Constraints()[0] {
			t.Errorf("%s: subtype is not k's constraint", n)
		}
	}
	for _, n := range []string{"u", "w", "r"} {
		if x := types[n]; !x.isDynamic() {
			t.Errorf("%s: subtype is not run-time constrained", n)
		}
	}
	// An inferred subtype refines the declared one: same base,
	// resolution and element subtype.
	for _, n := range []string{"s", "u", "v", "w"} {
		if types[n].Base() != types["k"].Base() {
			t.Errorf("%s: base type changed", n)
		}
	}

	checkExact(t, []wantCase{
		{name: "no value", src: lines(
			"entity e is end entity;",
			"architecture a of e is",
			"  signal s : bit_vector;",
			"begin",
			"end architecture;",
		), want: []string{
			"3: a signal cannot be of the unconstrained type " +
				"BIT_VECTOR",
		}},
		{name: "before 2019", std: "2008", src: lines(
			"entity e is end entity;",
			"architecture a of e is",
			"begin",
			"  process",
			"    variable v : bit_vector := \"10\";",
			"  begin",
			"    v := (others => '0');",
			"    wait;",
			"  end process;",
			"end architecture;",
		), want: []string{
			// Without inference the subtype stays unconstrained:
			// others has no range to fill (unsupported.go reports
			// the declaration itself as syntax/standard).
			"7: others needs an index range from the context, and " +
				"BIT_VECTOR is unconstrained",
		}},
	})
}

func TestEVariablePort(t *testing.T) {
	checkExact(t, []wantCase{
		{name: "legal", src: ePkg + lines(
			"use work.pk.all;",
			"entity dut is port (variable c : inout pt); end entity;",
			"architecture a of dut is",
			"begin",
			"  process begin c.inc; wait; end process;",
			"end architecture;",
			"use work.pk.all;",
			"entity mid is port (variable c : inout pt); end entity;",
			"architecture a of mid is",
			"begin",
			"  u : entity work.dut port map (c => c);",
			"end architecture;",
			"use work.pk.all;",
			"entity tb is end entity;",
			"architecture a of tb is",
			"  shared variable sv : pt;",
			"begin",
			"  u1 : entity work.dut port map (c => sv);",
			"  u2 : entity work.mid port map (sv);",
			"  u3 : entity work.dut;",
			"end architecture;",
		)},
		{name: "declaration", src: ePkg + lines(
			"use work.pk.all;",
			"entity dut is port (",
			"  variable a : inout integer;",
			"  variable b : in pt;",
			"  variable c : pt;",
			"  variable d : buffer pt);",
			"end entity;",
		), want: []string{
			"18: a variable port must be of a protected type, not " +
				"INTEGER",
			"19: a variable port must have mode inout",
			"20: a variable port must have mode inout",
			"21: a variable port must have mode inout",
		}},
		{name: "actual", src: ePkg + lines(
			"use work.pk.all;",
			"entity dut is port (variable c : inout pt); end entity;",
			"architecture a of dut is begin end architecture;",
			"use work.pk.all;",
			"entity tb is end entity;",
			"architecture a of tb is",
			"  shared variable sv : pt;",
			"  signal s : integer;",
			"begin",
			"  u1 : entity work.dut port map (c => open);",
			"  u2 : entity work.dut port map (c => s);",
			"end architecture;",
		), want: []string{
			"25: the actual of variable port c must be a shared " +
				"variable or a variable port",
			"26: expected pt, found INTEGER",
		}},
	})
}

func TestEProtectedComposites(t *testing.T) {
	checkExact(t, []wantCase{
		{name: "legal", src: ePkg + lines(
			"use work.pk.all;",
			"entity tb is end entity;",
			"architecture a of tb is",
			"  shared variable sa : pt_arr;",
			"  shared variable sr : pt_rec;",
			"begin",
			"  process",
			"    variable va : pt_arr;",
			"    variable p : pt_ptr := new pt;",
			"  begin",
			"    va(0).inc; sa(1).inc; sr.a.inc; p.inc;",
			"    sr.n := va(0).get;",
			"    wait;",
			"  end process;",
			"end architecture;",
		)},
		{name: "objects", src: ePkg + lines(
			"use work.pk.all;",
			"entity tb is",
			"  generic (g : pt_rec);",
			"  port (s : in pt_arr);",
			"end entity;",
			"architecture a of tb is",
			"  signal s2 : pt_arr;",
			"  constant c : pt_rec := (a => 0, n => 1);",
			"begin",
			"end architecture;",
		), want: []string{
			"18: a generic cannot be of the type pt_rec, which has " +
				"a subelement of a protected type",
			"19: a port cannot be of the type pt_arr, which has a " +
				"subelement of a protected type",
			"22: a signal cannot be of the type pt_arr, which has a " +
				"subelement of a protected type",
			"23: a constant cannot be of the type pt_rec, which has " +
				"a subelement of a protected type",
			"23: expected pt, found universal_integer",
		}},
		{name: "values", src: ePkg + lines(
			"use work.pk.all;",
			"entity tb is end entity;",
			"architecture a of tb is",
			"begin",
			"  process",
			"    variable x, y : pt;",
			"    variable va, vb : pt_arr;",
			"    variable vr : pt_rec;",
			"    variable vc : pt_arr := va;",
			"    variable b : boolean;",
			"  begin",
			"    va := vb;",
			"    va := (others => x);",
			"    va(0) := y;",
			"    x := y;",
			"    vr := vr when b else vr;",
			"    b := va = vb;",
			"    b := vr /= vr;",
			"    wait;",
			"  end process;",
			"end architecture;",
		), want: []string{
			"24: a variable of the type pt_arr, which has a " +
				"subelement of a protected type, cannot have an " +
				"initial value",
			"27: a variable of the type pt_arr, which has a " +
				"subelement of a protected type, cannot be assigned",
			"28: a variable of the type pt_arr, which has a " +
				"subelement of a protected type, cannot be assigned",
			"29: a variable of the protected type pt cannot be " +
				"assigned",
			"30: a variable of the protected type pt cannot be " +
				"assigned",
			"31: a variable of the type pt_rec, which has a " +
				"subelement of a protected type, cannot be assigned",
			"32: no = matches: ...",
			"33: no /= matches: ...",
		}},
		{name: "shared before 2019", std: "2008", src: lines(
			"package pk is",
			"  type pt is protected procedure inc; end protected;",
			"  type pt_arr is array (0 to 1) of pt;",
			"  shared variable sa : pt_arr;",
			"end package;",
		), want: []string{
			"4: the type of a shared variable must be a protected " +
				"type, not pt_arr",
		}},
	})
}

func TestEProtectedFormals(t *testing.T) {
	src := ePkg + lines(
		"use work.pk.all;",
		"package fs is",
		"  impure function peek(c : pt) return integer;",
		"  impure function deref(p : pt_ptr) return integer;",
		"  impure function first(a : pt_arr) return integer;",
		"  function plain(x : integer) return integer;",
		"end package;",
		"package body fs is",
		"  impure function peek(c : pt) return integer is",
		"  begin return c.get; end function;",
		"  impure function deref(p : pt_ptr) return integer is",
		"  begin return p.get; end function;",
		"  impure function first(a : pt_arr) return integer is",
		"  begin return a(0).get; end function;",
		"  function plain(x : integer) return integer is",
		"  begin return x; end function;",
		"end package body;",
	)
	r := analyzeOpen(t, "2019", src)
	if es := errorLines(r); len(es) != 0 {
		t.Fatalf("errors: %v", es)
	}
	got := map[string]ObjClass{}
	for _, u := range r.Units {
		for _, d := range u.Decls {
			if d.Kind == DeclFunction && d.Spec == nil {
				for _, p := range d.Params {
					got[string(p.Name)] = p.Class
				}
			}
		}
	}
	want := map[string]ObjClass{"c": ClassVariable, "p": ClassVariable,
		"a": ClassVariable, "x": ClassConstant}
	for n, c := range want {
		if got[n] != c {
			t.Errorf("parameter %s has class %v, want %v", n, got[n], c)
		}
	}

	checkExact(t, []wantCase{
		{name: "explicit class", src: ePkg + lines(
			"use work.pk.all;",
			"package fs is",
			"  impure function f1(constant c : pt) return integer;",
			"  impure function f2(signal c : pt) return integer;",
			"  impure function f3(variable c : inout pt) "+
				"return integer;",
			"  impure function f4(constant a : pt_arr) return integer;",
			"end package;",
		), want: []string{
			"18: a constant parameter cannot be of the protected " +
				"type pt",
			"19: a signal parameter cannot be of the protected type " +
				"pt",
			"21: a constant parameter cannot be of the type pt_arr, " +
				"which has a subelement of a protected type",
		}},
		{name: "default class before 2019", std: "2008", src: lines(
			"package pk is",
			"  type pt is protected procedure inc; end protected;",
			"  procedure p1(c : pt);",
			"  procedure p2(variable c : inout pt);",
			"end package;",
		), want: []string{
			"3: a constant parameter cannot be of the protected " +
				"type pt",
		}},
	})
}

func TestEPrivateVariable(t *testing.T) {
	src := lines(
		"package pv is",
		"  type inner is protected procedure inc; end protected;",
		"  type outer is protected",
		"    private variable iv : inner;",
		"    private variable n, m : integer := 3;",
		"    procedure bump;",
		"  end protected;",
		"end package;",
		"package body pv is",
		"  type inner is protected body",
		"    procedure inc is begin end procedure;",
		"  end protected body;",
		"  type outer is protected body",
		"    variable k : integer;",
		"    procedure bump is begin iv.inc; n := m + k; end;",
		"  end protected body;",
		"end package body;",
	)
	r := analyzeOpen(t, "2019", src)
	if es := errorLines(r); len(es) != 0 {
		t.Fatalf("errors: %v", es)
	}
	var priv, other []string
	for _, u := range r.Units {
		for _, d := range u.Decls {
			if privateVariable(d) {
				priv = append(priv, string(d.Name))
			} else if d.Kind == DeclVariable {
				other = append(other, string(d.Name))
			}
		}
	}
	if g := strings.Join(priv, " "); g != "iv n m" {
		t.Errorf("private variables %q, want \"iv n m\"", g)
	}
	if g := strings.Join(other, " "); g != "k" {
		t.Errorf("other variables %q, want \"k\"", g)
	}
	// The body sees the private variables: iv, n and m resolve.
	for _, w := range []string{"iv", "n", "m"} {
		d := declOfLine(r, 15, w)
		if !privateVariable(d) {
			t.Errorf("%s on line 15 does not denote the private "+
				"variable", w)
		}
	}
}

// declOfLine returns the declaration a reference to name on line ln of
// t.vhd denotes.
func declOfLine(r *Result, ln int, name string) *Decl {
	f := r.byPath["t.vhd"]
	for _, u := range r.Units {
		for _, ref := range u.Refs {
			if ref.Decl == nil || string(ref.Decl.Name) != name {
				continue
			}
			l, _ := f.Lines.LineCol(ref.Span.Start)
			if l == ln {
				return ref.Decl
			}
		}
	}
	return nil
}
