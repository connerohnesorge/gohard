package elab

// x_fix_test.go: design-region constants whose value the interpreter
// computes (interp_rtconst.go) and textio read failures under the
// VHDL-2019 read severity (interp_textio.go readFailed).

import (
	"fmt"
	"strings"
	"testing"
)

// TestXRuntimeConstants: an architecture constant initialised from an
// impure function or a std.env native is elaborated once per
// instance, in its instance, with its declaration's FILE_LINE; a
// constant reading it follows.
func TestXRuntimeConstants(t *testing.T) {
	for n := 1; n <= 4; n++ {
		var insts strings.Builder
		var want []string
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&insts, "  u%d : entity work.sub generic map "+
				"(step => %d);\n", i, i*10)
		}
		src := `use std.env.all;
package p is
  constant pv : string := VHDL_VERSION;
end package;

use std.env.all;
entity sub is
  generic (step : integer);
end entity;
architecture a of sub is
  type ctr_t is protected
    impure function bump(k : integer) return integer;
  end protected;
  type ctr_t is protected body
    variable count : integer := 0;
    impure function bump(k : integer) return integer is
    begin
      count := count + k;
      return count;
    end function;
  end protected body;
  shared variable ctr : ctr_t;
  impure function bump return integer is
  begin
    return ctr.bump(step);
  end function;
  constant c1 : integer := bump;
  constant c2 : integer := bump + c1;
  constant l  : positive := FILE_LINE;
  constant t  : string := TOOL_NAME & integer'image(c2);
begin
  process
  begin
    report t & " " & integer'image(c1) & " " & integer'image(l);
    report integer'image(ctr.bump(0));
    wait;
  end process;
end architecture;

use work.p.all;
entity top is end entity;
architecture a of top is
begin
` + insts.String() + `  process
  begin
    report pv;
    wait;
  end process;
end architecture;
`
		r := gSim(t, []gSource{{"t.vhd", src}}, "top", EnvOptions{})
		for i := 1; i <= n; i++ {
			s := i * 10
			want = append(want, fmt.Sprintf("gohard%d %d 29", 3*s, s),
				fmt.Sprint(2*s))
		}
		got := map[string]int{} // lookup only
		for _, m := range r.msgs {
			got[m]++
		}
		if len(r.errs) != 0 {
			t.Fatalf("n=%d: %q", n, r.errs)
		}
		if got["2019"] != 1 {
			t.Errorf("n=%d: package constant: %q", n, r.msgs)
		}
		for _, w := range want {
			if got[w] != 1 {
				t.Errorf("n=%d: no %q in %q", n, w, r.msgs)
			}
		}
		if len(r.msgs) != 2*n+1 {
			t.Errorf("n=%d: %q", n, r.msgs)
		}
	}
}

// TestXReadSeverity: a failed READ without GOOD is an assertion of the
// read severity (error by default) and the run goes on; the line is
// left as it was.
func TestXReadSeverity(t *testing.T) {
	src := `use std.textio.all;
use std.env.all;
entity tb is end entity;
architecture a of tb is
begin
  process
    variable l : line;
    variable i : integer;
  begin
    l := new string'("abc");
    read(l, i);
    SetVhdlReadSeverity(warning);
    read(l, i);
    report "after " & l.all & " " &
      integer'image(GetVhdlAssertCount(error)) &
      integer'image(GetVhdlAssertCount(warning));
    wait;
  end process;
end architecture;
`
	r := gSim(t, []gSource{{"tb.vhd", src}}, "tb", EnvOptions{})
	if len(r.errs) != 1 || !strings.Contains(r.errs[0],
		"assertion error: integer read failed") {
		t.Errorf("errors %q", r.errs)
	}
	r.errs = nil
	r.want(t, "integer read failed", "integer read failed", "after abc 11")
}
