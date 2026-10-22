package elab

import (
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
)

// s2_generic_test.go holds the simulator regression tests of the v1.1
// Stage 2 fixes to subprogram instances: the is <> default seen where
// the call is made, an operator symbol formal, array-class generic
// types indexed in a procedure that waits, a generic package formal
// reported as unsupported (please report) instead of failing as a
// runtime error, and an access to a protected type declared
// incomplete first.

// TestS2InstanceSim: the instances run in a process with NVC 1.23's
// output (recorded in testdata/conformance/generic_subprogram).
func TestS2InstanceSim(t *testing.T) {
	src := `
use std.textio.all;
package p is
  function mx
    generic (type t; function gt (a, b : t) return boolean is <>)
    parameter (a, b : t) return t;
  function mo
    generic (type t; function ">" (a, b : t) return boolean is <>)
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
  function mo
    generic (type t; function ">" (a, b : t) return boolean is <>)
    parameter (a, b : t) return t is
  begin
    if a > b then
      return a;
    end if;
    return b;
  end function;
end package body;
use std.textio.all;
use work.p.all;
entity s2 is end entity;
architecture a of s2 is
  type iv is array (natural range <>) of integer;
  function gt (x, y : integer) return boolean is
  begin
    return x < y;
  end function;
  function lt (x, y : integer) return boolean is
  begin
    return x < y;
  end function;
  procedure swp
    generic (type t is array (natural range <>) of integer)
    parameter (variable a : inout t) is
    variable tmp : integer;
  begin
    tmp := a(a'left);
    wait for 1 ns;
    a(a'left) := a(a'right);
    a(a'right) := tmp;
  end procedure;
begin
  process
    variable v : iv(0 to 2) := (9, 8, 7);
    variable l : line;
  begin
    write(l, mx generic map (t => integer) (1, 2));
    write(l, string'(" "));
    write(l, mo generic map (t => integer, ">" => lt) (3, 9));
    write(l, string'(" "));
    write(l, mo generic map (t => integer) (3, 9));
    swp generic map (t => iv) (v);
    write(l, string'(" "));
    write(l, v(0));
    write(l, v(2));
    writeline(output, l);
    wait;
  end process;
end architecture;
`
	for _, m := range gcModes {
		r := kSim(t, src, "s2", kOpts{gc: m.gc})
		if len(r.diags) > 0 || !slices.Equal(r.out, []string{"1 3 9 79"}) {
			t.Fatalf("%s: %v %q", m.name, r.diags, r.out)
		}
	}
}

// TestS2GenericPackageFormal: a subprogram instance with a generic
// package formal is reported as unsupported/construct (please report)
// when it is called, in a 2008 instance and a 2019 call, and the run
// ends there.
func TestS2GenericPackageFormal(t *testing.T) {
	const pkg = `
package gp is
  generic (k : integer);
end package;
package gp3 is new work.gp generic map (k => 3);
entity l is end entity;
architecture a of l is
  function sc
    generic (package p is new work.gp generic map (<>))
    parameter (x : integer) return integer is
  begin
    return p.k * x;
  end function;
  function sc3 is new sc generic map (p => work.gp3);
begin
  process
    variable v : integer := 1;
  begin
`
	for _, c := range []struct{ std, call string }{
		{"2008", "v := sc3(5);"},
		{"2019", "v := sc generic map (p => work.gp3) (5);"},
	} {
		src := pkg + "    " + c.call + `
    report "unreachable";
    wait;
  end process;
end architecture;
`
		r := kSim(t, src, "l", kOpts{std: c.std})
		want := []string{string(diag.UnsupportedConstruct) +
			": generic package formals of subprograms is not supported " +
			"yet, please report"}
		if !slices.Equal(r.diags, want) {
			t.Errorf("%s: %q, want %q", c.std, r.diags, want)
		}
	}
}

// TestS2IncompleteProtected: an access type to a protected type
// declared incomplete first (type pt; type pa is access pt) allocates
// objects of the full type, whose body runs.
func TestS2IncompleteProtected(t *testing.T) {
	src := `
use std.textio.all;
entity ip is end entity;
architecture a of ip is
  type pt;
  type pa is access pt;
  type pt is protected
    impure function get return integer;
  end protected pt;
  type pt is protected body
    variable n : integer := 41;
    impure function get return integer is
    begin
      n := n + 1;
      return n;
    end function get;
  end protected body pt;
begin
  process
    variable v : pa := new pt;
    variable l : line;
  begin
    write(l, v.get);
    write(l, v.all.get);
    writeline(output, l);
    wait;
  end process;
end architecture;
`
	for _, std := range []string{"2008", "2019"} {
		r := kSim(t, src, "ip", kOpts{std: std})
		if len(r.diags) > 0 || !slices.Equal(r.out, []string{"4243"}) {
			t.Errorf("%s: %v %q", std, r.diags, r.out)
		}
	}
}
