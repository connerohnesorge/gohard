package elab

import (
	"fmt"
	"strings"
	"testing"
)

// TestCondReturn runs the VHDL-2019 conditional return (LRM 10.13) in
// the interpreter, for x from -3 to 12: a function's `return v when
// c`, a conditional value with an unaffected branch (no return, the
// statements after it run), and a procedure's plain `return when c`.
// Each result equals the Go model of the same function.
func TestCondReturn(t *testing.T) {
	src := `
entity tb is end entity tb;
architecture sim of tb is
  impure function clip (x : integer) return integer is
  begin
    return 0 when x < 0;
    return x;
  end function clip;

  impure function band (x : integer) return integer is
  begin
    return 1 when x < 3 else unaffected when x < 6 else 3 when x < 9;
    return 2 when x < 6;
    return 4;
  end function band;

  procedure count (x : integer; variable n : inout integer) is
  begin
    n := n + 1;
    return when x mod 2 = 0;
    n := n + 10;
  end procedure count;
begin
  process
    variable n : integer;
  begin
    for x in -3 to 12 loop
      n := 0;
      count(x, n);
      report integer'image(x) & ": " & integer'image(clip(x)) & " " &
        integer'image(band(x)) & " " & integer'image(n);
    end loop;
    wait;
  end process;
end architecture sim;
`
	r, _ := simulate19(t, src, "tb")
	var want []string
	for x := -3; x <= 12; x++ {
		clip := max(x, 0)
		band := 4
		switch {
		case x < 3:
			band = 1
		case x < 6:
			band = 2
		case x < 9:
			band = 3
		}
		n := 1
		if x%2 != 0 {
			n = 11
		}
		want = append(want, fmt.Sprintf(
			"hint sim/assert-note report note: %d: %d %d %d", x, clip,
			band, n))
	}
	r.wantNotes(t, want...)
}

// TestCondReturnNoBranch: when no condition holds the statement does
// nothing, and a function that then ends without a return is a
// runtime error, as for a plain function.
func TestCondReturnNoBranch(t *testing.T) {
	src := `
entity tb is end entity tb;
architecture sim of tb is
  impure function f (x : integer) return integer is
  begin
    return 1 when x = 1 else 2 when x = 2;
  end function f;
begin
  process
  begin
    report integer'image(f(2));
    report integer'image(f(5));
    wait;
  end process;
end architecture sim;
`
	r, _ := simulate19(t, src, "tb")
	got := r.notes()
	if len(got) != 2 || got[0] != "hint sim/assert-note report note: 2" ||
		!strings.Contains(got[1], "ended without a return statement") {
		t.Errorf("diagnostics %q", got)
	}
}
