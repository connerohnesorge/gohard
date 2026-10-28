package elab

import (
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
)

// s3_generic_test.go holds the simulator regression tests of the
// second round of v1.1 Stage 2 fixes: the implicit operations of a
// VHDL-2019 generic type of a class run in instances (LRM 6.5.3), and
// a run-time read of a package instance's generic is reported as an
// unsupported construct (please report) instead of failing as a
// runtime error.

// TestS3ClassOpsSim: the class operations run in a process, in a
// VHDL-2008 style instance, in calls with a generic map and in a
// procedure that waits (its generic type bound in its frame), with
// NVC 1.23's output (testdata/conformance/generic_subprogram/
// class_ops), under every collector mode.
func TestS3ClassOpsSim(t *testing.T) {
	src := `
use std.textio.all;
entity s3 is end entity;
architecture a of s3 is
  function dbl generic (type t is range <>) (x : t) return t is
  begin
    return x + x;
  end function;
  function lt generic (type t is range <>) (x, y : t) return boolean is
  begin
    return x < y;
  end function;
  procedure acc generic (type t is units <>)
    parameter (variable s : inout t; d : t) is
  begin
    wait for 1 ns;
    s := s + d - d / 2;
  end procedure;
  procedure take generic (type d is private; type t is access d)
    (variable p : inout t; variable r : out d) is
  begin
    r := p.all;
    deallocate(p);
  end procedure;
  function di is new dbl generic map (t => integer);
  type ia is access integer;
begin
  process
    variable l : line;
    variable s : time := 1 ns;
    variable p : ia := new integer'(9);
    variable r : integer;
  begin
    write(l, di(21));
    write(l, string'(" "));
    write(l, boolean'image(lt generic map (t => integer) (2, 1)));
    acc generic map (t => time) (s, 4 ns);
    write(l, string'(" "));
    write(l, time'image(s));
    take generic map (d => integer, t => ia) (p, r);
    write(l, string'(" "));
    write(l, r);
    write(l, string'(" "));
    write(l, boolean'image(p = null));
    writeline(output, l);
    wait;
  end process;
end architecture;
`
	for _, m := range gcModes {
		r := kSim(t, src, "s3", kOpts{std: "2019", gc: m.gc})
		want := []string{"42 false 3000000 fs 9 true"}
		if len(r.diags) > 0 || !slices.Equal(r.out, want) {
			t.Fatalf("%s: %v %q, want %q", m.name, r.diags, r.out, want)
		}
	}
}

// TestS3PackageInstanceGeneric: reading a package instance's generic
// in a process (directly, through a constant of the package, and in a
// call with a generic map of one of its subprograms) is reported as
// unsupported/construct (please report), and the run ends there.
func TestS3PackageInstanceGeneric(t *testing.T) {
	const pkg = `
package gp is
  generic (k : integer);
  constant c : integer := k * 2;
  function mul generic (j : integer) (x : integer) return integer;
end package;
package body gp is
  function mul generic (j : integer) (x : integer) return integer is
  begin
    return x * j + k;
  end function;
end package body;
package gp3 is new work.gp generic map (k => 3);
use work.gp3.all;
entity t2 is end entity;
architecture a of t2 is
begin
  process
    variable v : integer;
  begin
`
	for _, read := range []string{"v := work.gp3.k;", "v := c;",
		"v := mul generic map (j => 2) (5);"} {
		src := pkg + "    " + read + `
    report "unreachable";
    wait;
  end process;
end architecture;
`
		r := kSim(t, src, "t2", kOpts{std: "2019"})
		want := []string{string(diag.UnsupportedConstruct) +
			": generics of package instances read at run time is not " +
			"supported yet, please report"}
		if !slices.Equal(r.diags, want) || len(r.out) != 0 {
			t.Errorf("%s: %q %q, want %q", read, r.diags, r.out, want)
		}
	}
}
