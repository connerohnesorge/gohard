package sema

import (
	"strings"
	"testing"
)

// TestSameFileReplace: within one design file a later unit replaces
// the earlier unit of the same name (LRM 13.1, 13.5): dependents see
// the later one and nothing is reported, for primary and secondary
// units; the same unit in two files stays library/duplicate-unit.
func TestSameFileReplace(t *testing.T) {
	r := analyze(t, map[string]string{"o.vhd": `package p is
  constant old_only : integer := 1;
end package;
package p is
  constant x : boolean := true;
end package;
entity e is end;
architecture a of e is begin end;
architecture a of e is
  signal s : boolean := work.p.x;
begin end;
use work.p.all;
entity top is end;
architecture t of top is
  constant y : boolean := x;
begin
  u: entity work.e(a);
end;
`}, nil)
	noErrors(t, r)
	f, _ := r.File("o.vhd")
	var hidden []string
	for _, u := range f.Units {
		if u.Duplicate {
			hidden = append(hidden, u.Label())
		}
	}
	if got := strings.Join(hidden, " "); got != "work.p work.e(a)" {
		t.Errorf("hidden units = %q, want the earlier p and e(a)", got)
	}
	// The replaced package's declarations are gone.
	r = analyze(t, map[string]string{"o.vhd": `package p is
  constant old_only : integer := 1;
end package;
package p is
end package;
use work.p.all;
entity top is end;
architecture t of top is
  constant y : integer := old_only;
begin end;
`}, nil)
	if got := diagText(r); !strings.Contains(got,
		"old_only is not declared") {
		t.Errorf("replaced package still visible:\n%s", got)
	}
	// A secondary unit before the replacing primary unit belongs to
	// the replaced one (textual analysis order).
	noErrors(t, analyze(t, map[string]string{"d.vhd": `entity dff is
  port (clk : in bit);
end;
architecture a of dff is
  signal s : bit;
begin
  s <= clk;
end;
entity dff is
end;
architecture a of dff is
begin
end;
`}, nil))
	// Two files: an error naming both, in either path order.
	for _, names := range [][2]string{{"a.vhd", "b.vhd"},
		{"b.vhd", "a.vhd"}} {
		r = analyze(t, map[string]string{
			names[0]: "package p is end;\n",
			names[1]: "package p is end;\n"}, nil)
		if got := diagText(r); !strings.Contains(got,
			"library/duplicate-unit") {
			t.Errorf("two files %v: want duplicate-unit, got\n%s",
				names, got)
		}
	}
}
