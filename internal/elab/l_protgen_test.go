package elab

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
)

// l_protgen_test.go holds the simulator tests of the generic clauses
// on protected types of v1.1 Stage 3 (#18, VHDL-2019 LRM 5.6.2,
// 5.6.4, 6.4.2.4): each object binds its own generics, from its
// variable's generic map, its instantiated type's map, the type's
// header map or the defaults, and its methods read only those.

// lCounter is a package with a generic protected counter: step has a
// default, size (a default reading step) too, elem none.
const lCounter = `
use std.textio.all;
package pk is
  type counter is protected
    generic (step : integer := 1; size : integer := step * 10;
             type elem);
    procedure bump;
    impure function get return integer;
    procedure put(x : elem);
    impure function last return elem;
    impure function scaled(x : integer) return integer;
    impure function width return integer;
  end protected;
end package;
package body pk is
  type counter is protected body
    variable n : integer := 0;
    variable l : elem;
    type buf_t is array (0 to size - 1) of elem;
    variable buf : buf_t;
    function scale(x : integer) return integer is
    begin
      return x * step;
    end function;
    procedure bump is begin n := n + step; end procedure;
    impure function get return integer is begin return n; end function;
    procedure put(x : elem) is
    begin
      l := x;
      buf(n mod size) := x;
    end procedure;
    impure function last return elem is begin return l; end function;
    impure function scaled(x : integer) return integer is
    begin
      return scale(x);
    end function;
    impure function width return integer is
    begin
      return buf'length;
    end function;
  end protected body;
end package body;
`

// lWrite is a procedure writing its integer arguments as one line.
const lWrite = `
  procedure say(a, b, c, d, e : integer) is
    variable l : line;
  begin
    write(l, a); write(l, string'(" ")); write(l, b);
    write(l, string'(" ")); write(l, c); write(l, string'(" "));
    write(l, d); write(l, string'(" ")); write(l, e);
    writeline(output, l);
  end procedure;
`

// TestLProtGenericObjects: objects of one generic protected type
// declared with different generic maps (a variable's, a shared
// variable's, an instantiated type's, the defaults) bind their own
// values, also as elements of an array, through a pointer and in a
// type declared in the body (size); under every collector mode.
func TestLProtGenericObjects(t *testing.T) {
	src := lCounter + `
use std.textio.all;
use work.pk.all;
entity tb is end entity;
architecture a of tb is
  type by3 is new counter generic map (step => 3, elem => boolean);
  type by3_ptr is access by3;
  type by3_arr is array (0 to 1) of by3;
  shared variable sw : counter generic map (5, 2, character);
` + lWrite + `
begin
  process
    variable c1 : counter generic map (step => 2, elem => integer);
    variable c0 : counter generic map (elem => bit);
    variable c3 : by3;
    variable p : by3_ptr;
    variable arr : by3_arr;
  begin
    p := new by3;
    c1.bump; c1.bump; c0.bump; c3.bump; sw.bump; p.bump; arr(1).bump;
    say(c1.get, c0.get, c3.get, sw.get, p.get);
    say(arr(0).get, arr(1).get, c1.width, c0.width, sw.width);
    say(c3.width, p.width, arr(1).width, c1.scaled(7), sw.scaled(7));
    c1.put(42); c3.put(true); sw.put('x');
    say(c1.last, boolean'pos(c3.last), character'pos(sw.last), 0, 0);
    wait;
  end process;
end architecture;
`
	want := []string{"4 1 3 5 3", "0 3 20 10 2", "30 30 30 14 35",
		"42 1 120 0 0"}
	for _, m := range gcModes {
		r := kSim(t, src, "tb", kOpts{gc: m.gc})
		if len(r.diags) > 0 || !slices.Equal(r.out, want) {
			t.Fatalf("%s: %v %q, want %q", m.name, r.diags, r.out, want)
		}
	}
}

// TestLProtGenericMemo is a property: random calls of a method whose
// pure helper function reads the object's generic (a memoizable call
// of the same function on the same argument) return, for each of
// three objects with different generics, what a Go model computes:
// no recorded call leaks one object's generic into another's.
func TestLProtGenericMemo(t *testing.T) {
	steps := []int{2, 3, 5}
	for seed := range uint64(6) {
		r := &env.SplitMix64{State: seed}
		var body strings.Builder
		var want []string
		for range 30 {
			o, x := env.Intn(r, len(steps)), env.Intn(r, 4)
			fmt.Fprintf(&body, "    say(o%d.scaled(%d), 0, 0, 0, 0);\n",
				o, x)
			want = append(want, fmt.Sprintf("%d 0 0 0 0", x*steps[o]))
		}
		src := lCounter + `
use std.textio.all;
use work.pk.all;
entity tb is end entity;
architecture a of tb is
` + lWrite + `
begin
  process
    variable o0 : counter generic map (step => 2, elem => bit);
    variable o1 : counter generic map (step => 3, elem => bit);
    variable o2 : counter generic map (step => 5, elem => bit);
  begin
` + body.String() + `
    wait;
  end process;
end architecture;
`
		res := kSim(t, src, "tb", kOpts{})
		if len(res.diags) > 0 || !slices.Equal(res.out, want) {
			t.Fatalf("seed %d: %v\n%q\nwant\n%q", seed, res.diags,
				res.out, want)
		}
	}
}

// TestLProtGenericType: the generic types of a protected type are
// bound per object: a range class type with its arithmetic (a value
// out of the actual's range is an error) and an array class type,
// for the object's variables, method parameters and results.
func TestLProtGenericType(t *testing.T) {
	src := `
use std.textio.all;
entity tb is end entity;
architecture a of tb is
  type acc is protected
    generic (type num is range <>; type vec is array (natural range <>)
             of type is private);
    procedure add(x : num);
    impure function total return num;
    procedure keep(v : vec);
    impure function kept return integer;
  end protected;
  type acc is protected body
    variable sum : num := 0;
    variable len : integer := 0;
    procedure add(x : num) is begin sum := sum + x * 2; end procedure;
    impure function total return num is begin return sum; end function;
    procedure keep(v : vec) is
    begin
      len := v'length;
    end procedure;
    impure function kept return integer is begin return len; end function;
  end protected body;
  type small is range 0 to 100;
  type ivec is array (natural range <>) of integer;
` + lWrite + `
begin
  process
    variable a : acc generic map (num => integer, vec => bit_vector);
    variable b : acc generic map (num => small, vec => ivec);
    variable bv : bit_vector(0 to 2) := "101";
    variable iv : ivec(1 to 5) := (1, 2, 3, 4, 5);
  begin
    a.add(20); a.add(1); b.add(4);
    a.keep(bv); b.keep(iv);
    say(a.total, integer(b.total), a.kept, b.kept, 0);
    b.add(48);
    say(integer(b.total), 0, 0, 0, 0);
    wait;
  end process;
end architecture;
`
	r := kSim(t, src, "tb", kOpts{})
	want := []string{"42 8 3 5 0"}
	if !slices.Equal(r.out, want) || len(r.diags) != 1 ||
		!strings.Contains(r.diags[0], "sim/runtime-error") ||
		!strings.Contains(r.diags[0], "104") {
		t.Fatalf("%v %q, want %q and a range error of 104", r.diags,
			r.out, want)
	}
}

// TestLProtGenericNested: a protected object holding an object of a
// generic protected type whose generic map reads the outer object's
// generics (a constant and a type), and a protected type declared with
// a generic map in its header (instantiated in place).
func TestLProtGenericNested(t *testing.T) {
	src := lCounter + `
use std.textio.all;
use work.pk.all;
entity tb is end entity;
architecture a of tb is
  type outer is protected
    generic (k : integer; type t);
    procedure tick;
    impure function inner_get return integer;
    impure function inner_width return integer;
  end protected;
  type outer is protected body
    variable inner : counter generic map (step => k * 10, size => k,
                                          elem => t);
    procedure tick is begin inner.bump; end procedure;
    impure function inner_get return integer is
    begin
      return inner.get;
    end function;
    impure function inner_width return integer is
    begin
      return inner.width;
    end function;
  end protected body;
  type fixed is protected
    generic (base : integer);
    generic map (base => 7);
    impure function get return integer;
  end protected;
  type fixed is protected body
    impure function get return integer is begin return base; end function;
  end protected body;
` + lWrite + `
begin
  process
    variable x : outer generic map (k => 2, t => integer);
    variable y : outer generic map (k => 4, t => character);
    variable f : fixed;
  begin
    x.tick; y.tick; y.tick;
    say(x.inner_get, y.inner_get, x.inner_width, y.inner_width, f.get);
    wait;
  end process;
end architecture;
`
	r := kSim(t, src, "tb", kOpts{})
	want := []string{"20 80 2 4 7"}
	if len(r.diags) > 0 || !slices.Equal(r.out, want) {
		t.Fatalf("%v %q, want %q", r.diags, r.out, want)
	}
}

// TestLProtGenericSubprogram: a protected type with a generic
// subprogram, which the simulator does not bind yet, is reported as
// unsupported/construct (please report) when its object is created,
// and the run ends there.
func TestLProtGenericSubprogram(t *testing.T) {
	src := `
entity tb is end entity;
architecture a of tb is
  type pt is protected
    generic (function f (x : integer) return integer);
    impure function get return integer;
  end protected;
  type pt is protected body
    impure function get return integer is begin return f(1); end function;
  end protected body;
  function inc (x : integer) return integer is
  begin
    return x + 1;
  end function;
begin
  process
    variable v : pt generic map (f => inc);
  begin
    report "unreachable";
    wait;
  end process;
end architecture;
`
	r := kSim(t, src, "tb", kOpts{})
	want := []string{string(diag.UnsupportedConstruct) + ": generic " +
		"subprograms of protected types is not supported yet, please " +
		"report"}
	if !slices.Equal(r.diags, want) {
		t.Fatalf("%q, want %q", r.diags, want)
	}
}

// TestLProtGenericActualType: an actual whose type only the generic
// map of the object gives (an aggregate for a parameter of a generic
// type) takes the object's actual type: sema substitutes it into the
// method's profile (methodsubst.go), so the call runs.
func TestLProtGenericActualType(t *testing.T) {
	src := `
entity tb is end entity;
architecture a of tb is
  type acc is protected
    generic (type vec);
    procedure keep(v : vec);
  end protected;
  type acc is protected body
    procedure keep(v : vec) is begin end procedure;
  end protected body;
  type ivec is array (natural range <>) of integer;
begin
  process
    variable b : acc generic map (vec => ivec);
  begin
    b.keep((1, 2, 3));
    report "reached";
    wait;
  end process;
end architecture;
`
	r := kSim(t, src, "tb", kOpts{})
	want := []string{"sim/assert-note: report note: reached"}
	if !slices.Equal(r.diags, want) {
		t.Fatalf("%q, want %q", r.diags, want)
	}
}
