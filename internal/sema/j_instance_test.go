package sema

import (
	"fmt"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
)

// j_instance_test.go tests the execution of subprogram instances by
// the static interpreter (interp.go): VHDL-2008 instantiation
// declarations and VHDL-2019 calls with a generic map, generic
// constants, types and subprograms bound by position, and the memo
// key that holds the bindings (memo.go).

// jGenPkg declares uninstantiated subprograms in a package with their
// bodies in the package body, and 2008 instances of them.
const jGenPkg = `
package p is
  type color is (red, green, blue);
  function scale
    generic (k : integer)
    parameter (x : integer) return integer;
  function offset
    generic (k : integer := 10; m : integer := k + 1)
    parameter (x : integer) return integer;
  function ident
    generic (type t)
    parameter (x : t) return t;
  function hi
    generic (type t is (<>))
    return t;
  function pick
    generic (type t; function better (a, b : t) return boolean)
    parameter (a, b : t) return t;
  function largest
    generic (type t; function "<" (a, b : t) return boolean is <>)
    parameter (a, b, c : t) return t;
  function closer (a, b : integer) return boolean;
  procedure add
    generic (step : integer)
    parameter (variable acc : inout integer);
  function sum_add (n : integer) return integer;

  function triple is new scale generic map (k => 3);
  function off_dflt is new offset;
  function off_5 is new offset generic map (k => 5);
  function ident_color is new ident generic map (t => color);
  function max_int is new pick generic map (t => integer, better => ">");
  function nearest is new pick generic map (t => integer,
    better => closer);
  function hi_color is new hi generic map (t => color);

  constant c_triple  : integer := triple(7);
  constant c_dflt    : integer := off_dflt(1);
  constant c_off5    : integer := off_5(1);
  constant c_color   : color := ident_color(green);
  constant c_max     : integer := max_int(3, 9);
  constant c_nearest : integer := nearest(-7, 4);
  constant c_hi      : color := hi_color;
  constant c_call    : integer := scale generic map (k => 4) (x => 5);
  constant c_call2   : integer := scale generic map (6) (5) +
    scale generic map (k => 2) (5);
  constant c_bool    : boolean := ident generic map (t => boolean) (true);
  constant c_real    : real := pick generic map (t => real,
    better => "<") (2.5, 1.5);
  constant c_large   : integer := largest generic map (t => integer)
    (4, 11, 6);
  constant c_char    : character := largest generic map (t => character)
    ('q', 'z', 'a');
  constant c_proc    : integer := sum_add(3);
end package;

package body p is
  function scale
    generic (k : integer)
    parameter (x : integer) return integer is
  begin
    return k * x;
  end function;

  function offset
    generic (k : integer := 10; m : integer := k + 1)
    parameter (x : integer) return integer is
  begin
    return 100 * k + m + x;
  end function;

  function ident
    generic (type t)
    parameter (x : t) return t is
    variable tmp : t;
  begin
    tmp := x;
    return tmp;
  end function;

  function hi
    generic (type t is (<>))
    return t is
  begin
    return t'high;
  end function;

  function pick
    generic (type t; function better (a, b : t) return boolean)
    parameter (a, b : t) return t is
  begin
    if better(a, b) then
      return a;
    end if;
    return b;
  end function;

  function largest
    generic (type t; function "<" (a, b : t) return boolean is <>)
    parameter (a, b, c : t) return t is
    variable m : t := a;
  begin
    if m < b then
      m := b;
    end if;
    if m < c then
      m := c;
    end if;
    return m;
  end function;

  function closer (a, b : integer) return boolean is
  begin
    return abs a < abs b;
  end function;

  procedure add
    generic (step : integer)
    parameter (variable acc : inout integer) is
  begin
    acc := acc + step;
  end procedure;

  function sum_add (n : integer) return integer is
    variable acc : integer := 0;
  begin
    for i in 1 to n loop
      add generic map (step => 10 * i) parameter map (acc);
    end loop;
    return acc;
  end function;
end package body;
`

// TestJInstanceStatic: instances evaluate statically with their
// generics bound: constants (named, positional, defaults reading
// earlier generics), types, subprograms (named, an operator symbol,
// is <>), and generic maps on calls inside interpreted bodies whose
// actuals are loop parameters.
func TestJInstanceStatic(t *testing.T) {
	r := analyzeOpen(t, "2019", jGenPkg)
	if errs := errorLines(r); len(errs) > 0 {
		t.Fatalf("errors: %q", errs)
	}
	dWant(t, "2019", r, map[string]string{
		"c_triple": "21", "c_dflt": "1012", "c_off5": "507",
		"c_color": "green", "c_max": "9", "c_nearest": "4",
		"c_hi": "blue", "c_call": "20", "c_call2": "40",
		"c_bool": "true", "c_real": "1.5", "c_large": "11",
		"c_char": "'z'", "c_proc": "60",
	})
}

// TestJInstanceOf: only subprograms with generic associations are
// instances; an instance is as pure as what it instantiates and runs
// its body.
func TestJInstanceOf(t *testing.T) {
	r := analyzeOpen(t, "2019", jGenPkg)
	l, _ := r.Library("work")
	u, _ := l.Unit("p")
	ev := NewEvaluator(r, 0)
	look := func(n Key) *Decl {
		ds := u.Region.Lookup(n)
		if len(ds) == 0 {
			t.Fatalf("no %s", n)
		}
		return ds[0]
	}
	for _, n := range []Key{"triple", "max_int", "hi_color"} {
		x := look(n)
		g := InstanceOf(x)
		if g == nil || g == x || g.Generics != nil {
			t.Fatalf("%s: InstanceOf = %v", n, g)
		}
		if !Pure(x) || Pure(x) != g.Pure {
			t.Errorf("%s: Pure = %v, %s's %v", n, Pure(x), g.Text, g.Pure)
		}
		if b := ev.Body(x); b == nil || b != ev.Body(g) || b == g {
			t.Errorf("%s: Body is not %s's body", n, g.Text)
		}
	}
	for _, n := range []Key{"scale", "pick", "closer", "c_triple"} {
		if g := InstanceOf(look(n)); g != nil {
			t.Errorf("%s: InstanceOf = %v, want nil", n, g.Text)
		}
	}
	// The actual of a generic subprogram is resolved by the profile.
	x := look("nearest")
	body := ev.Body(x)
	var better *Decl
	for _, d := range genericsOf(body.Region) {
		if d.Kind == DeclGenericSubprogram {
			better = d
		}
	}
	if a := ev.InstanceSubprogram(x, better); a != look("closer") {
		t.Errorf("nearest.better = %v, want closer", a)
	}
	if a := ev.InstanceSubprogram(look("max_int"), better); a == nil ||
		!a.Implicit || a.Name != `">"` {
		t.Errorf("max_int.better = %v, want the predefined \">\"", a)
	}
}

// jScalePkg returns a package of constants scale generic map (k => K)
// (x => X) for the pairs, called through a pure wrapper so a call
// site repeats with other generics.
func jScalePkg(pairs [][2]int64) string {
	var b strings.Builder
	b.WriteString(`package p is
  function scale
    generic (k : integer)
    parameter (x : integer) return integer;
  function twice
    generic (k : integer)
    parameter (x : integer) return integer;
  function run (k, x : integer) return integer;
`)
	for i, p := range pairs {
		fmt.Fprintf(&b, "  constant c%d : integer := run(%d, %d);\n", i,
			p[0], p[1])
	}
	b.WriteString(`end package;
package body p is
  function scale
    generic (k : integer)
    parameter (x : integer) return integer is
  begin
    return k * x;
  end function;
  function twice
    generic (k : integer)
    parameter (x : integer) return integer is
  begin
    return scale generic map (k => k) (x) +
      scale generic map (k => k) (x);
  end function;
  function run (k, x : integer) return integer is
  begin
    return twice generic map (k => k) (x => x);
  end function;
end package body;
`)
	return b.String()
}

// TestJInstanceMemoProperty: memoized evaluation (WithMemo, as in the
// simulator) gives every call of an instance the value and the step
// count of plain evaluation, whatever the order and repetition of the
// generic actuals: the memo key holds the bindings.
func TestJInstanceMemoProperty(t *testing.T) {
	rng := dRng(18)
	for round := range 6 {
		n := 4 + rng.intn(20)
		pairs := make([][2]int64, n)
		for i := range pairs {
			// Few distinct values, so keys repeat.
			pairs[i] = [2]int64{int64(rng.intn(5)) - 2,
				int64(rng.intn(4))}
		}
		r := analyzeOpen(t, "2019", jScalePkg(pairs))
		if errs := errorLines(r); len(errs) > 0 {
			t.Fatalf("errors: %q", errs)
		}
		plain, memo := NewEvaluator(r, 0), NewEvaluator(r, 0).WithMemo()
		for i, p := range pairs {
			name := fmt.Sprintf("c%d", i)
			v1, err1 := dConst(t, plain, r, name)
			s1 := plain.Used()
			v2, err2 := dConst(t, memo, r, name)
			s2 := memo.Used()
			if err1 != nil || err2 != nil {
				t.Fatalf("round %d %s: %v, %v", round, name, err1, err2)
			}
			want := 2 * p[0] * p[1]
			if v1.I != want || v2.I != want {
				t.Errorf("round %d run(%d, %d) = %d (memo %d), want %d",
					round, p[0], p[1], v1.I, v2.I, want)
			}
			if s1 != s2 {
				t.Errorf("round %d run(%d, %d): %d steps, memoized %d",
					round, p[0], p[1], s1, s2)
			}
		}
		if len(memo.sh.memo) == 0 {
			t.Errorf("round %d: no instance call was memoized", round)
		}
	}
}

// TestJMemoKey: the memo key of an instance call holds its bindings:
// equal bindings and arguments give equal keys, other generic values
// or types other keys, and bindings that cannot be keyed no key.
func TestJMemoKey(t *testing.T) {
	x := &Decl{Unit: &Unit{Index: 3}, Span: cst.Range{Start: 40}}
	in := func(k int64, lt *eval.Type) *Instance {
		key := appendDecl(nil, x)
		n := 0
		key, _ = appendValue(append(key, '|'), eval.Int(eval.Integer, k),
			&n)
		key = appendLayout(append(key, '|'), lt, 0)
		return &Instance{key: string(key)}
	}
	args := []eval.Value{eval.Int(eval.Integer, 4)}
	key := func(i *Instance) string {
		k, ok := memoKey(x, i, args)
		if !ok {
			t.Fatal("no key")
		}
		return k
	}
	a, b := key(in(3, eval.Integer)), key(in(3, eval.Integer))
	if a != b {
		t.Errorf("equal bindings: %q != %q", a, b)
	}
	if key(in(4, eval.Integer)) == a {
		t.Error("another generic value has the same key")
	}
	if key(in(3, eval.Real)) == a {
		t.Error("another generic type has the same key")
	}
	if plain, _ := memoKey(x, nil, args); plain == a {
		t.Error("an instance call has the key of a plain call")
	}
	if _, ok := memoKey(x, &Instance{}, args); ok {
		t.Error("unkeyable bindings have a key")
	}
	sub := eval.String.ArraySubtype(eval.Range{Left: 1, Right: 3})
	other := eval.String.ArraySubtype(eval.Range{Left: 1, Right: 4})
	if string(appendLayout(nil, sub, 0)) ==
		string(appendLayout(nil, other, 0)) {
		t.Error("subtypes of other bounds have the same layout key")
	}
}
