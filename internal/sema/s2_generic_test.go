package sema

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/eval"
)

// s2_generic_test.go holds the regression tests of the v1.1 Stage 2
// fixes to subprogram instances (LRM 4.4, 9.3.4): a generic subprogram
// whose designator is an operator symbol named as a formal ("<" =>
// f), an is <> default resolved where the instance is made (LRM
// 6.5.6.2), objects of an array-class generic type indexed and sliced
// (LRM 6.5.3), the associations a generic map may not hold (LRM
// 6.5.7.1, 6.5.7.2) and a protected body declared after an incomplete
// type declaration.

// s2OpPkg declares mx, whose generic subprogram is the operator ">".
const s2OpPkg = `
package p is
  function mx
    generic (type t; function ">" (a, b : t) return boolean is <>)
    parameter (a, b : t) return t;
end package;
package body p is
  function mx
    generic (type t; function ">" (a, b : t) return boolean is <>)
    parameter (a, b : t) return t is
  begin
    if a > b then
      return a;
    end if;
    return b;
  end function;
end package body;
`

// TestS2OperatorFormal: an operator symbol names a generic subprogram
// formal in a 2008 instance and a 2019 call (NVC 1.23 alike: imn=3,
// mxc=3, mxd=9); the actual replaces the default.
func TestS2OperatorFormal(t *testing.T) {
	src := s2OpPkg + `
use work.p.all;
package q is
  function lt (a, b : integer) return boolean;
  function imn is new mx generic map (t => integer, ">" => lt);
  function imx is new mx generic map (t => integer);
  constant c_imn : integer := imn(3, 9);
  constant c_imx : integer := imx(3, 9);
  constant c_mxc : integer := mx generic map (t => integer, ">" => lt)
    (3, 9);
  constant c_mxd : integer := mx generic map (t => integer) (3, 9);
  constant c_upper : integer := mx generic map (t => integer, "<" => lt)
    (3, 9);
end package;
package body q is
  function lt (a, b : integer) return boolean is
  begin
    return a < b;
  end function;
end package body;
`
	r := analyzeOpen(t, "2019", src)
	errs := errorLines(r)
	want := []string{`29: "<" is not a generic of mx`}
	if !slices.Equal(errs, want) {
		t.Fatalf("errors %q, want %q", errs, want)
	}
	s2Want(t, r, "q", map[string]string{"c_imn": "3", "c_imx": "9",
		"c_mxc": "3", "c_mxd": "9"})
	// A string literal that is no operator symbol is no formal.
	r = analyzeOpen(t, "2019", s2OpPkg+`
use work.p.all;
package q is
  constant c : integer := mx generic map (t => integer, "xy" => mx) (1, 2);
end package;
`)
	if errs := errorLines(r); len(errs) != 1 ||
		!strings.Contains(errs[0], `"xy" is not a formal designator`) {
		t.Errorf("errors %q", errs)
	}
}

// TestS2BoxDefaultSite: an is <> default denotes the subprogram
// visible where the instance is made (LRM 6.5.6.2): declared in the
// region of the call or the 2008 instance, or made visible there by a
// use clause, not only where the generic subprogram is declared.
func TestS2BoxDefaultSite(t *testing.T) {
	src := `
package p is
  function mx
    generic (type t; function gt (a, b : t) return boolean is <>)
    parameter (a, b : t) return t;
end package;
package body p is
  function mx
    generic (type t; function gt (a, b : t) return boolean is <>)
    parameter (a, b : t) return t is
  begin
    if gt(a, b) then
      return a;
    end if;
    return b;
  end function;
end package body;
package ops is
  function gt (x, y : integer) return boolean;
end package;
package body ops is
  function gt (x, y : integer) return boolean is
  begin
    return x < y;
  end function;
end package body;
use work.p.all;
package local is
  function gt (x, y : integer) return boolean;
  function mi is new mx generic map (t => integer);
  constant c_call : integer := mx generic map (t => integer) (1, 2);
  constant c_inst : integer := mi(1, 2);
end package;
package body local is
  function gt (x, y : integer) return boolean is
  begin
    return x < y;
  end function;
end package body;
use work.p.all;
use work.ops.all;
package used is
  constant c_used : integer := mx generic map (t => integer) (5, 7);
end package;
`
	r := analyzeOpen(t, "2019", src)
	if errs := errorLines(r); len(errs) > 0 {
		t.Fatalf("errors: %q", errs)
	}
	s2Want(t, r, "local", map[string]string{"c_call": "1", "c_inst": "1"})
	s2Want(t, r, "used", map[string]string{"c_used": "5"})
}

// TestS2ArrayClass: an object of an array-class generic type is
// indexed and sliced with the index subtype of the generic type, and
// its attributes have that subtype; the subtypes named in the generic
// type keep their own names.
func TestS2ArrayClass(t *testing.T) {
	src := `
package p is
  type iv is array (natural range <>) of integer;
  function sum
    generic (type t is array (natural range <>) of integer)
    parameter (a : t) return integer;
  function upd
    generic (type el; type t is array (natural range <>) of el)
    parameter (a : t; x : el) return t;
  function sumi is new sum generic map (t => iv);
  constant c_sum  : integer := sumi((4, 5, 6));
  constant c_call : integer := sum generic map (t => iv) ((9, 8, 7));
  constant c_upd  : iv := upd generic map (el => integer, t => iv)
    ((1, 2, 3), 7);
end package;
package body p is
  function sum
    generic (type t is array (natural range <>) of integer)
    parameter (a : t) return integer is
    variable s : integer := 0;
  begin
    for i in a'range loop
      s := s + a(i);
    end loop;
    return 100 * s + a(a'left to a'left + 1)(a'left + 1);
  end function;
  function upd
    generic (type el; type t is array (natural range <>) of el)
    parameter (a : t; x : el) return t is
    variable r : t(a'range) := a;
  begin
    r(r'right) := x;
    return r;
  end function;
end package body;
`
	r := analyzeOpen(t, "2019", src)
	if errs := errorLines(r); len(errs) > 0 {
		t.Fatalf("errors: %q", errs)
	}
	dWant(t, "2019", r, map[string]string{"c_sum": "1505",
		"c_call": "2408", "c_upd": "(1,2,7)"})
	nat := 0
	for _, u := range r.Units {
		walkUnitExprs(u, func(e *Expr) {
			if e.Kind == ExprAttr && e.Attr == "left" && e.Type != nil {
				if n := e.Type.Name(); n != "NATURAL" {
					t.Errorf("a'left is %s, want NATURAL", n)
				}
				nat++
			}
		})
	}
	if nat == 0 {
		t.Error("no a'left found")
	}
	// An index of the wrong type is an error, as for any array.
	r = analyzeOpen(t, "2019", `
package p is
  function f
    generic (type t is array (natural range <>) of integer)
    parameter (a : t) return integer;
end package;
package body p is
  function f
    generic (type t is array (natural range <>) of integer)
    parameter (a : t) return integer is
  begin
    return a(true) + a(1, 2);
  end function;
end package body;
`)
	errs := errorLines(r)
	if len(errs) != 2 {
		t.Errorf("errors %q, want two", errs)
	}
}

// s2Want evaluates the constants of want (name -> image) declared in
// package unit of library work.
func s2Want(t *testing.T, r *Result, unit string,
	want map[string]string,
) {
	t.Helper()
	ev := NewEvaluator(r, 0)
	l, _ := r.Library("work")
	u, ok := l.Unit(Key(unit))
	if !ok {
		t.Fatalf("no unit %s", unit)
	}
	for _, name := range slices.Sorted(maps.Keys(want)) {
		ds := u.Region.Lookup(Key(name))
		if len(ds) == 0 {
			t.Fatalf("no constant %s", name)
		}
		v, err := ev.Constant(ds[0])
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := eval.ImageOf(v); got != want[name] {
			t.Errorf("%s = %s, want %s", name, got, want[name])
		}
	}
}

// walkUnitExprs calls f for every expression of the sequential
// statements of the subprogram bodies declared in u.
func walkUnitExprs(u *Unit, f func(*Expr)) {
	if u.Region == nil {
		return
	}
	var sub func(e *Expr)
	sub = func(e *Expr) {
		if e == nil {
			return
		}
		f(e)
		sub(e.Prefix)
		for _, a := range e.Args {
			sub(a)
		}
	}
	for _, d := range u.Region.Decls {
		walkSeq(d.Seq, func(*SeqStmt) {}, sub)
	}
}

// TestS2GenericMapAssocs: a generic map of a subprogram call, a
// subprogram instance or a package instance associates each generic
// once (LRM 6.5.7.1), and the actual of a generic type is a type or
// subtype (LRM 6.5.7.2); NVC 1.23 rejects the same sources.
func TestS2GenericMapAssocs(t *testing.T) {
	checkExact(t, []wantCase{
		{name: "legal", src: gcUse(
			"    v := scale generic map (k => 3) (4);",
			"    v := ident generic map (t => natural) (4);",
		)},
		{name: "illegal", src: gcUse(
			"    v := ident generic map (t => v) (4);",
			"    add generic map (k => 1, k => 2) (v);",
			"    v := scale generic map (3, k => 3) (4);",
			"    v := ident generic map (t => 5) (4);",
		), want: []string{
			"27: v does not denote a type or a subtype",
			"28: generic k already has an actual",
			"29: generic k already has an actual",
			"30: 5 does not denote a type or a subtype",
		}},
		{name: "instances", src: gcPkg + lines(
			"package q is",
			"  function s2 is new work.p.scale generic map (k => 1, k => 2);",
			"end package;",
			"package g is",
			"  generic (k : integer);",
			"end package;",
			"package g2 is new work.g generic map (k => 1, k => 2);",
		), want: []string{
			"20: generic k already has an actual",
			"25: generic k already has an actual",
		}},
	})
}
