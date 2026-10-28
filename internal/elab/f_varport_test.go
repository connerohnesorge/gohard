package elab

import (
	"fmt"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/elab/hier"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

const fCounterPkg = `
package counter_pkg is
  type counter_t is protected
    procedure inc;
    impure function get return integer;
  end protected counter_t;
end package counter_pkg;

package body counter_pkg is
  type counter_t is protected body
    variable n : integer := 0;
    procedure inc is
    begin
      n := n + 1;
    end procedure inc;
    impure function get return integer is
    begin
      return n;
    end function get;
  end protected body counter_t;
end package body counter_pkg;

use work.counter_pkg.all;
entity dut is
  generic (times : natural := 3);
  port (variable c : inout counter_t);
end entity dut;

architecture rtl of dut is
begin
  process
  begin
    for i in 1 to times loop
      c.inc;
    end loop;
    wait;
  end process;
end architecture rtl;
`

// TestVarPortProtCond is the prot_cond shape: the DUT increments the
// testbench's shared counter three times through its variable port,
// in one delta, and the testbench reads 3.
func TestVarPortProtCond(t *testing.T) {
	src := fCounterPkg + `
use work.counter_pkg.all;
entity tb is end entity tb;
architecture sim of tb is
  shared variable cnt : counter_t;
begin
  dut : entity work.dut port map (c => cnt);
  process
  begin
    wait for 1 ns;
    report "cnt = " & integer'image(cnt.get);
    wait;
  end process;
end architecture sim;
`
	r, d := simulate19(t, src, "tb")
	r.wantNotes(t, "hint sim/assert-note report note: cnt = 3")
	dut := d.Top.Children[0]
	var port *sema.Decl
	for _, x := range dut.Entity.Region.Decls {
		if x.Kind == sema.DeclPort {
			port = x
		}
	}
	in, x, ok := d.VarPort(dut, port)
	if !ok || in != d.Top || x == nil || x.Kind != sema.DeclSharedVariable ||
		x.Text != "cnt" {
		t.Fatalf("VarPort = %v, %v, %v; want tb's shared variable cnt",
			in, x, ok)
	}
	for _, s := range d.Model.Signals {
		if strings.HasSuffix(s.Path, ".c") {
			t.Errorf("variable port elaborated as signal %s", s.Path)
		}
	}
}

// TestVarPortOwnObject: an unassociated variable port, and a variable
// port of the top, is an object of its own (the entity instance and the
// port). An explicit open actual is an error (sema, as in NVC).
func TestVarPortOwnObject(t *testing.T) {
	src := fCounterPkg + `
use work.counter_pkg.all;
entity tb is
  port (variable top : inout counter_t);
end entity tb;
architecture sim of tb is
begin
  u : entity work.dut generic map (times => 2);
  process
  begin
    top.inc;
    wait for 1 ns;
    report "top = " & integer'image(top.get);
    wait;
  end process;
end architecture sim;
`
	r, d := simulate19(t, src, "tb")
	r.wantNotes(t, "hint sim/assert-note report note: top = 1")
	for _, in := range []*hier.Instance{d.Top, d.Top.Children[0]} {
		for _, x := range in.Entity.Region.Decls {
			if x.Kind != sema.DeclPort {
				continue
			}
			bi, bx, ok := d.VarPort(in, x)
			if !ok || bi != in || bx != x {
				t.Errorf("%s.%s: VarPort = %v, %v, %v; want its own "+
					"object", in.Path, x.Text, bi, bx, ok)
			}
		}
	}
	if len(d.ownPorts) != 2 {
		t.Errorf("own port objects %v, want 2", d.ownPorts)
	}
}

// TestVarPortInstancesSeparate is a property: n DUT instances, each
// incrementing times_i times through its variable port, bound to one
// of m shared variables (directly, or through a forwarding instance
// with a variable port of its own), leave each shared variable at the
// sum of the times of the instances bound to it, whatever the binding.
func TestVarPortInstancesSeparate(t *testing.T) {
	rng := &env.SplitMix64{State: 18}
	for iter := range 12 {
		m := 1 + env.Intn(rng, 3)
		n := 1 + env.Intn(rng, 5)
		want := make([]int, m)
		var b strings.Builder
		b.WriteString(fCounterPkg + `
use work.counter_pkg.all;
entity fwd is
  generic (times : natural);
  port (variable c : inout counter_t);
end entity fwd;
architecture rtl of fwd is
begin
  inner : entity work.dut generic map (times => times)
    port map (c => c);
end architecture rtl;

use work.counter_pkg.all;
entity tb is end entity tb;
architecture sim of tb is
`)
		for j := range m {
			fmt.Fprintf(&b, "  shared variable v%d : counter_t;\n", j)
		}
		b.WriteString("begin\n")
		for i := range n {
			j, k := env.Intn(rng, m), env.Intn(rng, 5)
			want[j] += k
			ent := "dut"
			if env.Intn(rng, 2) == 0 {
				ent = "fwd"
			}
			fmt.Fprintf(&b, "  u%d : entity work.%s generic map "+
				"(times => %d) port map (c => v%d);\n", i, ent, k, j)
		}
		b.WriteString("  process\n  begin\n    wait for 1 ns;\n")
		var notes []string
		for j := range m {
			fmt.Fprintf(&b, "    report \"v%d = \" & "+
				"integer'image(v%d.get);\n", j, j)
			notes = append(notes, fmt.Sprintf(
				"hint sim/assert-note report note: v%d = %d", j, want[j]))
		}
		b.WriteString("    wait;\n  end process;\nend architecture sim;\n")
		t.Run(fmt.Sprint(iter), func(t *testing.T) {
			r, _ := simulate19(t, b.String(), "tb")
			r.wantNotes(t, notes...)
		})
	}
}
