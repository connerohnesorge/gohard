package sema

import (
	"strconv"
	"testing"
)

// TestGenericFormalConcreteFirst checks that inside an uninstantiated
// generic package a literal actual selects the concrete overload over
// one whose formal is of a generic type, which no literal is of (LRM
// 6.5.3), while two concrete homographs stay ambiguous.
func TestGenericFormalConcreteFirst(t *testing.T) {
	checkMarked(t, []markedCase{{name: "generic", std: "2008", src: `
package gp is
  generic (type t_element);
  function f(e : t_element; s : string) return integer;
  function f(i : integer; s : string) return integer;
  function h(e : t_element) return integer;
end package;
package body gp is
  function f(e : t_element; s : string) return integer is
  begin return 0; end function;
  function f(i : integer; s : string) return integer is
  begin return i; end function;
  function h(e : t_element) return integer is
  begin
    return f(1, "x") + f(e, "y");
  end function;
end package body;
`}, {name: "concrete", std: "2008", src: `
package p1 is
  function f(i : integer) return integer;
end package;
package p2 is
  function f(i : integer) return integer;
end package;
use work.p1.all, work.p2.all;
package p3 is
  constant c : integer := f(1); --E
end package;
`}})
}

// TestRangeTypeIsBase checks that an index range bounded by an object
// of a subtype is a range of the base type (LRM 5.2.1, 5.3.2.2): 0 to
// N with N POSITIVE is legal for every N, while a subtype indication
// of POSITIVE itself still rejects 0.
func TestRangeTypeIsBase(t *testing.T) {
	var cases []markedCase
	for _, n := range []int{1, 2, 10, 100, 2147483647} {
		for _, st := range []string{"positive", "natural"} {
			cases = append(cases, markedCase{
				name: st + strconv.Itoa(n), src: `
package p is
  constant N : ` + st + ` := ` + strconv.Itoa(n) + `;
  alias NA is N;
  type a1 is array (0 to N) of bit;
  type a2 is array (-1 to NA) of bit;
  subtype s1 is integer range 0 to N;
  procedure x;
end package;
package body p is
  type pt is protected
    procedure y;
  end protected;
  type pt is protected body
    type a3 is array (0 to NA) of bit;
    procedure y is begin end procedure;
  end protected body;
  procedure x is
    type a4 is array (0 to N) of bit;
  begin end procedure;
end package body;
`})
		}
	}
	cases = append(cases, markedCase{name: "subtype", src: `
package q is
  subtype s is positive range 0 to 3; --E
end package;
`})
	checkMarked(t, cases)
}
