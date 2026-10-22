package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/eval"
)

// staticPkg is a package whose constants exercise the interpreter:
// bundled ieee functions run from source and user functions with every
// sequential statement the interpreter models.
const staticPkg = `
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
package p is
  type rec is record a : integer; b : std_logic; end record;
  type mat is array (0 to 1, 0 to 2) of integer;
  function fact (n : natural) return natural;
  function clog2 (n : positive) return natural;
  function sum_to (n : natural) return natural;
  function odd_bits (v : std_logic_vector) return natural;
  function rev (v : std_logic_vector) return std_logic_vector;
  function pick (s : std_logic_vector(1 downto 0)) return integer;
  function swap (r : rec) return rec;
  function spin (n : natural) return natural;
  function deep (n : natural) return natural;
  function msum (m : mat) return integer;
  function cond_ret (n : integer) return integer;
  function with_proc (n : integer) return integer;
  function bad_index (n : natural) return natural;
  procedure twice (x : inout integer; y : out integer);

  constant u5    : unsigned(7 downto 0) := to_unsigned(5, 8);
  constant i10   : natural := to_integer(unsigned'("1010"));
  constant im3   : integer := to_integer(to_signed(-3, 4));
  constant add   : unsigned(3 downto 0) := unsigned'("0011") + "0101";
  constant addi  : unsigned(7 downto 0) := u5 + 250;
  constant rsz   : signed(7 downto 0) := resize(to_signed(-2, 4), 8);
  constant andv  : std_logic_vector(3 downto 0) := "1100" and "1010";
  constant shl   : unsigned(7 downto 0) := shift_left(u5, 2);
  constant eq    : boolean := to_unsigned(9, 4) = 9;
  constant slv   : std_logic_vector(7 downto 0) :=
    std_logic_vector(to_unsigned(200, 8));
  constant f5    : natural := fact(5);
  constant c1000 : natural := clog2(1000);
  constant s100  : natural := sum_to(100);
  constant odd   : natural := odd_bits("10110110");
  constant rv    : std_logic_vector(0 to 3) := rev("1101");
  constant pk    : integer := pick("10");
  constant sw    : rec := swap((a => 7, b => '1'));
  constant ms    : integer := msum(((1, 2, 3), (4, 5, 6)));
  constant cr    : integer := cond_ret(-4) + cond_ret(4);
  constant wp    : integer := with_proc(3);
  constant named : natural := to_integer(ARG => to_unsigned(SIZE => 6,
    ARG => 33));
end package;

package body p is
  function fact (n : natural) return natural is
  begin
    if n <= 1 then
      return 1;
    end if;
    return n * fact(n - 1);
  end function;

  function clog2 (n : positive) return natural is
    variable r : natural := 0;
    variable v : natural := n - 1;
  begin
    while v > 0 loop
      v := v / 2;
      r := r + 1;
    end loop;
    return r;
  end function;

  function sum_to (n : natural) return natural is
    variable s : natural := 0;
    variable i : natural := 0;
  begin
    loop
      i := i + 1;
      next when i mod 2 = 0 and false;
      s := s + i;
      exit when i = n;
    end loop;
    return s;
  end function;

  function odd_bits (v : std_logic_vector) return natural is
    variable n : natural := 0;
  begin
    outer: for i in v'range loop
      next outer when v(i) = '0';
      n := n + 1;
    end loop outer;
    return n;
  end function;

  function rev (v : std_logic_vector) return std_logic_vector is
    alias av : std_logic_vector(v'length - 1 downto 0) is v;
    variable r : std_logic_vector(0 to v'length - 1);
  begin
    for i in av'range loop
      r(i) := av(i);
    end loop;
    return r;
  end function;

  function pick (s : std_logic_vector(1 downto 0)) return integer is
  begin
    case s is
      when "00" => return 0;
      when "01" | "10" => return 1;
      when others => return 2;
    end case;
  end function;

  function swap (r : rec) return rec is
    variable o : rec := r;
  begin
    o.a := -r.a;
    o.b := not r.b;
    return o;
  end function;

  function spin (n : natural) return natural is
    variable i : natural := 0;
  begin
    while true loop
      i := (i + 1) mod 7;
    end loop;
    return i;
  end function;

  function deep (n : natural) return natural is
  begin
    return deep(n + 1);
  end function;

  function msum (m : mat) return integer is
    variable s : integer := 0;
  begin
    for i in m'range(1) loop
      for j in m'range(2) loop
        s := s + m(i, j) * (i + 1);
      end loop;
    end loop;
    return s;
  end function;

  function cond_ret (n : integer) return integer is
  begin
    if n < 0 then
      return -n;
    else
      return n * 10;
    end if;
  end function;

  procedure twice (x : inout integer; y : out integer) is
  begin
    x := x * 2;
    y := x + 1;
  end procedure;

  function with_proc (n : integer) return integer is
    variable a : integer := n;
    variable b : integer;
  begin
    twice(a, b);
    return a * 100 + b;
  end function;

  function bad_index (n : natural) return natural is
    variable v : bit_vector(0 to 3);
  begin
    v(n) := '1';
    return n;
  end function;
end package body;
`

func constDecl(t *testing.T, r *Result, name string) *Decl {
	t.Helper()
	l, ok := r.Library("work")
	if !ok {
		t.Fatal("no work library")
	}
	u, ok := l.Unit("p")
	if !ok {
		t.Fatal("no package p")
	}
	ds := u.Region.Lookup(Key(name))
	if len(ds) == 0 {
		t.Fatalf("no constant %s", name)
	}
	return ds[0]
}

func evalConst(t *testing.T, r *Result, name string) (eval.Value, error) {
	t.Helper()
	d := constDecl(t, r, name)
	ev := NewEvaluator(r, 0)
	v, err := ev.Value(d.Value)
	if err == nil {
		if lt, lerr := ev.Layout(d.Type); lerr == nil {
			v, err = eval.Check(v, lt)
		}
	}
	return v, err
}

// TestInterpret evaluates constants whose values come from bundled
// ieee functions and user functions interpreted from source.
func TestInterpret(t *testing.T) {
	r := analyze(t, map[string]string{"p.vhd": staticPkg}, nil)
	if d := diagText(r); d != "" {
		t.Fatalf("diagnostics:\n%s", d)
	}
	for _, c := range []struct{ name, want string }{
		{"u5", `"00000101"`},
		{"i10", "10"},
		{"im3", "-3"},
		{"add", `"1000"`},
		{"addi", `"11111111"`},
		{"rsz", `"11111110"`},
		{"andv", `"1000"`},
		{"shl", `"00010100"`},
		{"eq", "true"},
		{"slv", `"11001000"`},
		{"f5", "120"},
		{"c1000", "10"},
		{"s100", "5050"},
		{"odd", "5"},
		{"rv", `"1011"`},
		{"pk", "1"},
		{"sw", "(-7, '0')"},
		{"ms", "36"},
		{"cr", "44"},
		{"wp", "607"},
		{"named", "33"},
	} {
		v, err := evalConst(t, r, c.name)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := v.Image(); got != c.want {
			t.Errorf("%s = %s, want %s", c.name, got, c.want)
		}
	}
}

// callExpr returns a typed call of the package function fn on one
// integer literal, built from a constant of the package.
func evalCall(t *testing.T, r *Result, fn string, arg int64,
	steps int64,
) (eval.Value, int64, error) {
	t.Helper()
	l, _ := r.Library("work")
	u, _ := l.Unit("p")
	ds := u.Region.Lookup(Key(fn))
	if len(ds) == 0 {
		t.Fatalf("no function %s", fn)
	}
	f := ds[0]
	ev := NewEvaluator(r, steps)
	b := eval.NewBudget(steps)
	v, err := ev.Call(f, []eval.Value{eval.Int(eval.Integer, arg)}, b)
	return v, b.Used(), err
}

// TestInterpretLimits: a function that never returns and an unbounded
// recursion end with the step limit (P7), an index error inside a
// function is a design error, and a function without a body is not
// static.
func TestInterpretLimits(t *testing.T) {
	r := analyze(t, map[string]string{"p.vhd": staticPkg}, nil)
	for _, fn := range []string{"spin", "deep"} {
		_, _, err := evalCall(t, r, fn, 1, 100_000)
		if eval.CodeOf(err) != eval.ErrCodeBudget {
			t.Errorf("%s: %v, want the step limit", fn, err)
		}
	}
	_, _, err := evalCall(t, r, "bad_index", 9, 0)
	if eval.CodeOf(err) != eval.ErrCodeIndex {
		t.Errorf("bad_index(9): %v", err)
	}
	if v, _, err := evalCall(t, r, "bad_index", 2, 0); err != nil ||
		v.I != 2 {
		t.Errorf("bad_index(2) = %v, %v", v.Image(), err)
	}
}

// TestInterpretBudget: the step count of a call is a function of the
// call alone, and a budget of n steps fails exactly when the call needs
// more than n.
func TestInterpretBudget(t *testing.T) {
	r := analyze(t, map[string]string{"p.vhd": staticPkg}, nil)
	for _, c := range []struct {
		fn  string
		arg int64
	}{{"fact", 7}, {"clog2", 70000}, {"sum_to", 40}, {"cond_ret", 3}} {
		v1, n1, err1 := evalCall(t, r, c.fn, c.arg, 0)
		v2, n2, err2 := evalCall(t, r, c.fn, c.arg, 0)
		if err1 != nil || err2 != nil || !v1.Equal(v2) || n1 != n2 {
			t.Fatalf("%s: %v %v / %v %v steps %d %d", c.fn,
				v1.Image(), err1, v2.Image(), err2, n1, n2)
		}
		if _, _, err := evalCall(t, r, c.fn, c.arg, n1); err != nil {
			t.Errorf("%s with %d steps: %v", c.fn, n1, err)
		}
		_, _, err := evalCall(t, r, c.fn, c.arg, n1-1)
		if eval.CodeOf(err) != eval.ErrCodeBudget {
			t.Errorf("%s with %d steps: %v, want the limit", c.fn,
				n1-1, err)
		}
	}
}

// TestStaticCheckCalls: a package constant whose function call fails
// with a design error is reported, and one that exceeds the limit is
// the eval-limit warning.
func TestStaticCheckCalls(t *testing.T) {
	src := strings.Replace(staticPkg,
		"  constant named : natural",
		"  constant bad : natural := bad_index(4);\n"+
			"  constant slow : natural := spin(1);\n"+
			"  constant named : natural", 1)
	r := analyze(t, map[string]string{"p.vhd": src}, nil)
	got := diagText(r)
	for _, w := range []string{"sema/static-error: index 4 is out of " +
		"range 0 to 3", "sema/eval-limit: constant slow"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if n := strings.Count(got, "\n"); n != 2 {
		t.Errorf("want 2 diagnostics:\n%s", got)
	}
}

// TestFrameCaches: layouts and constants computed inside a call that
// depend on its parameters, also through subtypes of such subtypes, are
// never reused by a later call with other arguments; the evaluator of a
// unit evaluates each constant once.
func TestFrameCaches(t *testing.T) {
	r := analyze(t, map[string]string{"q.vhd": `
library ieee;
use ieee.numeric_std.all;
package q is
  function widen (v : bit_vector) return bit_vector;
  constant s1 : unsigned(7 downto 0) := unsigned'(x"1e") - unsigned'(x"2");
  constant s2 : unsigned(3 downto 0) := unsigned'(x"5") - unsigned'(x"3");
  constant s3 : unsigned(11 downto 0) := resize(s2, 12) + s1;
  constant w2 : bit_vector(1 downto 0) := widen("10");
  constant w5 : bit_vector(4 downto 0) := widen("10110");
  constant w3 : bit_vector(2 downto 0) := widen("011");
end package;
package body q is
  function widen (v : bit_vector) return bit_vector is
    subtype t is bit_vector(v'length - 1 downto 0);
    subtype t2 is t;
    variable r : t2;
  begin
    r := v;
    return r;
  end function;
end package body;
`}, nil)
	if d := diagText(r); d != "" {
		t.Fatalf("diagnostics:\n%s", d)
	}
	l, _ := r.Library("work")
	u, _ := l.Unit("q")
	ev := NewEvaluator(r, 0)
	for _, c := range []struct{ name, want string }{
		{"s1", `"00011100"`}, {"s2", `"0010"`}, {"s3", `"000000011110"`},
		{"w2", `"10"`}, {"w5", `"10110"`}, {"w3", `"011"`},
	} {
		d := u.Region.Lookup(Key(c.name))[0]
		v, err := ev.Constant(d)
		if err != nil || v.Image() != c.want {
			t.Errorf("%s = %s %v, want %s", c.name, v.Image(), err, c.want)
		}
	}
}

// gtype1 is NVC's test/bounds/gtype1.vhd (test_bounds.c test_gtype1):
// a VHDL-2019 array class generic type whose body constants use
// A'INDEX, A'INDEX'LOW and A'INDEX'HIGH.
const gtype1 = `package test_pkg is
  generic (
    type A is array(type is range <>) of type is private ;
    function "&"(L : A'element ; R : A) return A is <> ;
    function "+"(L : A'index ; R : integer) return A'index is <>
   ) ;
    function create_rt(size : natural ; init : A'element) return A ;
    function create_recurse(size : natural ; init : A'element) return A ;
end package ;

package body test_pkg is
    constant NULL_LOW : A'index := A'index'low when A'index'high - A'index'low > 0 else A'index'high ;
    constant NULL_HIGH : A'index := A'index'high when A'index'high - A'index'low > 0 else A'index'low ;
    subtype NULL_RANGE is A'index range NULL_HIGH to NULL_LOW ;

    function create_rt(size : natural ; init : A'element) return A is
        constant rv : A(A'index'low to A'index'low+size) := (others => init) ;
        variable nullarray : A(NULL_RANGE) ;
    begin
        return nullarray when size = 0 else rv ;
    end function ;

    function create_recurse(size : natural ; init : A'element) return A is
        variable nullarray : A(NULL_RANGE) ;
    begin
        return nullarray when size = 0 else
               A'(A'index'low => init) when size = 1 else
               init & create_recurse(size-1, init) ;
    end function ;
end package body ;
`

// TestPackageInstGenericArrayClass: the constants of a generic
// package instance's body are evaluated with the VHDL-2019 array class
// generic type bound to the actual, its 'INDEX and 'ELEMENT to the
// actual's: NULLINT (40 to 25) holds neither NULL_LOW (25) nor
// NULL_HIGH (40) in nulltest_pkg, named after its unit, while the
// legal instances and the uninstantiated package give no error.
func TestPackageInstGenericArrayClass(t *testing.T) {
	arch := `entity test is
end entity ;

architecture arch of test is
    subtype myint is integer range 25 to 40 ;
    type myarray is array(myint range <>) of real ;
    subtype nullint is integer range 40 to 25 ;
    type nullarray is array(nullint range <>) of real ;
    subtype singleint is integer range 42 downto 42 ;
    type singlearray is array(singleint range <>) of real ;
    package mytest_pkg is new work.test_pkg generic map(A => myarray) ;
    package nulltest_pkg is new work.test_pkg generic map (A => nullarray) ;
    package singletest_pkg is new work.test_pkg generic map (A => singlearray) ;
begin
end architecture ;
`
	r := analyze2019(t, map[string]string{"pkg.vhd": gtype1,
		"test.vhd": arch})
	want := "pkg.vhd:12:36: sema/static-error: constant NULL_LOW: value " +
		"25 is out of range 40 to 25 in work.test(arch).nulltest_pkg\n" +
		"pkg.vhd:13:37: sema/static-error: constant NULL_HIGH: value " +
		"40 is out of range 40 to 25 in work.test(arch).nulltest_pkg\n"
	if got := diagText(r); got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}

	// A library-level instance is named by its unit.
	r = analyze2019(t, map[string]string{"pkg.vhd": gtype1,
		"inst.vhd": `package types is
  subtype nullint is integer range 40 to 25 ;
  type nullarray is array(nullint range <>) of real ;
end package ;
package lib_null is new work.test_pkg
  generic map (A => work.types.nullarray) ;
`})
	got := diagText(r)
	if strings.Count(got, "\n") != 2 ||
		!strings.Contains(got, "constant NULL_LOW: value 25 is out of "+
			"range 40 to 25 in work.lib_null\n") {
		t.Errorf("diagnostics:\n%s", got)
	}
}
