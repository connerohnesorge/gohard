package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
)

// callModeSrc passes ports and view elements to procedure parameters
// of every mode; each line marked "bad" is an error both GHDL and NVC
// report (LRM 6.5.2, 6.5.7.1), the others are legal.
const callModeSrc = `entity c is port (i : in bit; o : out bit; io : inout bit);
end;
architecture a of c is
  procedure drive(signal x : out bit) is begin x <= '1'; end;
  procedure both(signal x : inout bit) is begin x <= '1'; end;
  procedure rd(signal x : in bit) is begin end;
  procedure vout(variable x : out bit) is begin x := '1'; end;
  procedure vin(x : in bit; variable y : out bit) is
  begin
    vout(x); -- bad: in parameter
    vout(y);
  end;
begin
  process
    variable v : bit;
  begin
    drive(i); -- bad
    both(i); -- bad
    rd(i);
    drive(o);
    both(io);
    rd(io);
    rd(o); -- 2008: legal; 1993: bad
    vout(v);
    wait;
  end process;
end;
`

func badLines(src string, std string) []int {
	var out []int
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "-- bad") ||
			(std == "1993" && strings.Contains(l, "1993: bad")) {
			out = append(out, i+1)
		}
	}
	return out
}

// TestCallActualMode: a write formal (out, inout) cannot take an
// interface object of mode in, and before VHDL-2008 a read formal
// cannot take a port of mode out; exactly the marked lines fail.
func TestCallActualMode(t *testing.T) {
	for _, std := range []string{"1993", "2008"} {
		r := analyze(t, map[string]string{"c.vhd": callModeSrc},
			func(c *config.Config) { c.Standard = std })
		var got []int
		for _, d := range r.Diagnostics {
			got = append(got, d.Line)
		}
		want := badLines(callModeSrc, std)
		if len(got) != len(want) {
			t.Fatalf("%s: got\n%swant lines %v", std, diagText(r), want)
		}
		for k := range got {
			if got[k] != want[k] {
				t.Fatalf("%s: got\n%swant lines %v", std, diagText(r),
					want)
			}
		}
	}
}

// TestCallActualView: an element of mode in of a mode view port, or a
// whole subview holding one, cannot be the actual of an out or inout
// signal parameter, as it cannot be an assignment target (#17).
func TestCallActualView(t *testing.T) {
	src := `use work.pkg.all;
entity e is port (p : view v; q : view ov); end;
architecture a of e is
  procedure drive(signal x : out bit) is begin x <= '1'; end;
  procedure both(signal x : inout bit) is begin end;
  procedure sub(signal x : out subr) is begin end;
  procedure rd(signal x : in bit) is begin end;
begin
  process begin
    drive(p.a);
    both(p.c);
    rd(p.b);
    drive(p.b); -- bad
    both(p.b); -- bad
    drive(q.s.x);
    drive(q.s.y); -- bad
    sub(q.s); -- bad
    wait;
  end process;
end;
`
	r := analyze2019(t, map[string]string{"p.vhd": viewPkg, "e.vhd": src})
	var got []int
	for _, d := range r.Diagnostics {
		if d.Path != "e.vhd" || !strings.Contains(d.Message,
			"has mode in in view") {
			t.Fatalf("unexpected:\n%s", diagText(r))
		}
		got = append(got, d.Line)
	}
	want := badLines(src, "2019")
	if len(got) != len(want) {
		t.Fatalf("got\n%swant lines %v", diagText(r), want)
	}
	for k := range got {
		if got[k] != want[k] {
			t.Fatalf("got\n%swant lines %v", diagText(r), want)
		}
	}
}
