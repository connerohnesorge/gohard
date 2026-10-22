package elab

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// j_generic_test.go runs subprogram instances in the interpreter
// (interp_call.go, interp_dyn.go): VHDL-2008 instantiation
// declarations and VHDL-2019 calls with a generic map, whose generic
// constants, types and subprograms are objects of the call's frame
// when the interpreter runs the body (procedures, impure functions)
// and the static interpreter's bindings when it does not; and method
// aliases, called on the object their prefix names.

// jNote is the diagnostic of report text.
func jNote(text string) string {
	return "hint sim/assert-note report note: " + text
}

// TestJGenericProcedureFrames: generics of procedures and impure
// functions the interpreter runs are bound per frame: a procedure that
// waits keeps its generics across the wait, nested instances of one
// subprogram with other generics do not see each other's, and a
// generic subprogram of the frame dispatches to its actual.
func TestJGenericProcedureFrames(t *testing.T) {
	src := `
entity tb is end entity tb;
architecture sim of tb is
  signal clk : bit := '0';

  procedure tick
    generic (type t; n : natural; function img (x : t) return string)
    parameter (signal c : inout bit; v : t) is
  begin
    for i in 1 to n loop
      c <= not c;
      wait for 1 ns;
      report "tick " & img(v) & " " & integer'image(i) & "/" &
        integer'image(n);
    end loop;
  end procedure tick;

  impure function depth
    generic (k : integer)
    parameter (x : integer) return integer is
  begin
    if x <= 0 then
      return k;
    end if;
    return k + depth generic map (k => k * 10) (x - 1);
  end function depth;

  function iimg (x : integer) return string is
  begin
    return integer'image(x);
  end function iimg;

  function bimg (x : bit) return string is
  begin
    return bit'image(x);
  end function bimg;

  procedure tick_bit is new tick generic map (t => bit, n => 2,
    img => bimg);
begin
  process
  begin
    tick generic map (t => integer, n => 2, img => iimg)
      parameter map (clk, 7);
    tick_bit(clk, '1');
    report "depth = " & integer'image(depth generic map (k => 1) (3));
    wait;
  end process;
end architecture sim;
`
	r, _ := simulate19(t, src, "tb")
	r.wantNotes(t,
		jNote("tick 7 1/2"), jNote("tick 7 2/2"),
		jNote("tick '1' 1/2"), jNote("tick '1' 2/2"),
		jNote("depth = 1111"))
}

// TestJDynamicGenericsProperty: for sequences of generic actuals
// (variables and loop parameters, repeating), each call of a pure
// instance, of a procedure instance and of an impure instance returns
// the Go model's value: the memoized pure calls keep the bindings
// apart. Two runs give the same trace.
func TestJDynamicGenericsProperty(t *testing.T) {
	seed := uint64(27)
	next := func(n int) int {
		seed += 0x9e3779b97f4a7c15
		z := seed
		z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
		z = (z ^ z>>27) * 0x94d049bb133111eb
		return int((z ^ z>>31) % uint64(n))
	}
	for round := range 4 {
		n := 3 + next(8)
		ks := make([]int, n)
		var lits []string
		for i := range ks {
			ks[i] = next(5) - 2
			lits = append(lits, fmt.Sprint(ks[i]))
		}
		src := `
entity tb is end entity tb;
architecture sim of tb is
  type iv is array (natural range <>) of integer;
  constant ks : iv := (` + strings.Join(lits, ", ") + `);

  function scale
    generic (k : integer)
    parameter (x : integer) return integer is
  begin
    return k * x;
  end function scale;

  impure function shift
    generic (type t; k : t; function plus (a, b : t) return t)
    parameter (x : t) return t is
  begin
    return plus(x, k);
  end function shift;

  procedure add
    generic (step : integer)
    parameter (variable acc : inout integer) is
  begin
    acc := acc + step;
  end procedure add;
begin
  process
    variable k, acc : integer := 0;
  begin
    for i in ks'range loop
      k := ks(i);
      add generic map (step => scale generic map (k => k) (i))
        parameter map (acc);
      report integer'image(i) & ": " &
        integer'image(scale generic map (k => k) (x => 3)) & " " &
        integer'image(shift generic map (t => integer, k => k,
          plus => "+") (i)) & " " & integer'image(acc);
    end loop;
    wait;
  end process;
end architecture sim;
`
		r, _ := simulate19(t, src, "tb")
		var want []string
		acc := 0
		for i, k := range ks {
			acc += k * i
			want = append(want, jNote(fmt.Sprintf("%d: %d %d %d", i, 3*k,
				i+k, acc)))
		}
		r.wantNotes(t, want...)
		again, _ := simulate19(t, src, "tb")
		if !bytes.Equal(r.text, again.text) {
			t.Errorf("round %d: the traces of two runs differ", round)
		}
	}
}

// TestJGenericTypes: the generic types of instances follow their
// actuals in subtypes, attributes and objects, in the static and in
// the running interpreter, including an instance declared in a
// process.
func TestJGenericTypes(t *testing.T) {
	src := `
entity tb is end entity tb;
architecture sim of tb is
  type color is (red, green, blue);

  function span_of
    generic (type t is (<>))
    return string is
  begin
    return t'image(t'low) & ".." & t'image(t'high);
  end function span_of;

  impure function fill
    generic (type t; n : positive)
    parameter (x : t) return string is
    type arr is array (1 to n) of t;
    variable a : arr := (others => x);
  begin
    return integer'image(a'length);
  end function fill;

  function low_color is new span_of generic map (t => color);
begin
  process
    subtype small is integer range 3 to 6;
    function low_small is new span_of generic map (t => small);
  begin
    report low_color & " " & low_small & " " &
      span_of generic map (t => boolean);
    report fill generic map (t => real, n => 4) (1.5) &
      fill generic map (t => color, n => 2) (red);
    wait;
  end process;
end architecture sim;
`
	r, _ := simulate19(t, src, "tb")
	r.wantNotes(t, jNote("red..blue 3..6 false..true"), jNote("42"))
}

// TestJMethodAlias: a method alias of a private variable (VHDL-2019)
// calls the method on that variable of the object it is called on,
// from outside the object and from its own body; each object has its
// own.
func TestJMethodAlias(t *testing.T) {
	src := `
entity tb is end entity tb;
architecture sim of tb is
  type counter_t is protected
    procedure incr (n : integer);
    impure function get return integer;
  end protected counter_t;

  type counter_t is protected body
    variable count : integer := 0;
    procedure incr (n : integer) is
    begin
      count := count + n;
    end procedure incr;
    impure function get return integer is
    begin
      return count;
    end function get;
  end protected body counter_t;

  type stats_t is protected
    private variable hits : counter_t;
    alias hit is hits.incr [integer];
    alias hit_count is hits.get [return integer];
    procedure hit_twice;
    impure function doubled return integer;
  end protected stats_t;

  type stats_t is protected body
    procedure hit_twice is
    begin
      hit(1);
      hit(1);
    end procedure hit_twice;
    impure function doubled return integer is
    begin
      return 2 * hit_count;
    end function doubled;
  end protected body stats_t;

  shared variable a, b : stats_t;
begin
  process
  begin
    a.hit(5);
    b.hit(1);
    a.hit_twice;
    report integer'image(a.hit_count) & " " &
      integer'image(b.hit_count) & " " & integer'image(a.doubled);
    wait;
  end process;
end architecture sim;
`
	r, _ := simulate19(t, src, "tb")
	r.wantNotes(t, jNote("7 1 14"))
}
