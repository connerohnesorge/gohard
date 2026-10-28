package sema

import (
	"slices"
	"strings"
	"testing"
)

// vunitConfigDesign binds unbound PSL verification units with use
// vunit (#66): a configuration binds v_top to tb(sim) and, by a
// component configuration, v_comp to u1 (dut(rtl)); a configuration
// specification in tb binds v_spec to u2. a_cfg.vhd and b_props.vhd
// sort before the units they bind to, so the vunits see those units
// only because they depend on them. v_alone is named nowhere, so its
// names are not visible (as GHDL 6.0.0 reports them); v_bound is bound
// in its declaration and so cannot be named in use vunit, and u_dut is
// an entity. Lines marked --E have an error.
var vunitConfigDesign = map[string]string{
	"a_cfg.vhd": `configuration cfg of tb is
  use vunit v_top;
  for sim
    for u1 : dut
      use entity work.dut(rtl);
      use vunit work.v_comp;
    end for;
  end for;
end configuration;
configuration bad of tb is
  use vunit v_bound; --E
  use vunit dut; --E
  for sim
  end for;
end configuration;
`,
	"b_props.vhd": `vunit v_top {
  default clock is rising_edge(clk);
  assert always (a -> next b);
}
vunit v_comp {
  default clock is rising_edge(clk);
  assert always (x -> y);
  assert always (q -> width = 4);
}
vunit v_spec {
  default clock is rising_edge(clk);
  assert always (x -> y);
}
vunit v_alone {
  default clock is rising_edge(clk); --E
}
vunit v_bound(dut(rtl)) {
  default clock is rising_edge(clk);
}
`,
	"c_tb.vhd": `library ieee;
use ieee.std_logic_1164.all;
entity tb is end entity;
architecture sim of tb is
  signal clk, a, b : std_logic := '0';
  component dut is
    port (clk : in std_logic; x, y : in std_logic);
  end component;
  for u2 : dut use entity work.dut(rtl);
    use vunit v_spec;
  end for;
begin
  u1 : dut port map (clk, a, b);
  u2 : dut port map (clk, b, a);
end architecture;
`,
	"d_dut.vhd": `library ieee;
use ieee.std_logic_1164.all;
entity dut is
  generic (width : natural := 4);
  port (clk : in std_logic; x, y : in std_logic);
end entity;
architecture rtl of dut is
  signal q : std_logic;
begin
  q <= x and y;
end architecture;
`,
}

// markedFileErrors checks that the error lines of r are exactly the lines
// of files marked --E.
func markedFileErrors(t *testing.T, r *Result, files map[string]string) {
	t.Helper()
	type line struct {
		path string
		n    int
	}
	var want, got []line
	for _, p := range slices.Sorted(mapsKeys(files)) {
		for i, l := range strings.Split(files[p], "\n") {
			if strings.Contains(l, "--E") {
				want = append(want, line{p, i + 1})
			}
		}
	}
	for _, d := range r.Diagnostics {
		if d.Severity.String() == "error" {
			got = append(got, line{d.Path, d.Line})
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("error lines %v, want %v\n%s", got, want, diagText(r))
	}
}

// TestVunitConfigBindings (#66): inside an unbound verification unit
// that a use vunit indication binds, the names of the design entity it
// binds to are visible: the tb signals for v_top, the dut ports,
// generic and rtl signal for v_comp and v_spec. The names of use vunit
// resolve to the units.
func TestVunitConfigBindings(t *testing.T) {
	r := analyze(t, vunitConfigDesign, nil)
	markedFileErrors(t, r, vunitConfigDesign)
	src := vunitConfigDesign["b_props.vhd"]
	for _, c := range []struct {
		at         int // occurrence index of name in b_props.vhd
		name, path string
		kind       DeclKind
		line       int // of the declaration
	}{
		{0, "clk", "c_tb.vhd", DeclSignal, 5},
		{0, "a -> next", "c_tb.vhd", DeclSignal, 5},
		{1, "clk", "d_dut.vhd", DeclPort, 5},
		{0, "x -> y", "d_dut.vhd", DeclPort, 5},
		{0, "q ->", "d_dut.vhd", DeclSignal, 8},
		{0, "width", "d_dut.vhd", DeclGeneric, 4},
		{2, "clk", "d_dut.vhd", DeclPort, 5},
		{1, "x -> y", "d_dut.vhd", DeclPort, 5},
	} {
		off := nthOffset(src, c.name, c.at)
		ref, ok := r.RefAt("b_props.vhd", off)
		if !ok || ref.Decl == nil {
			t.Errorf("%s #%d: no reference", c.name, c.at)
			continue
		}
		d := ref.Decl
		if l, _ := d.File.Lines.LineCol(d.Span.Start); d.Kind != c.kind ||
			d.File.Path != c.path || l != c.line {
			t.Errorf("%s #%d: %s at %s:%d, want %s at %s:%d", c.name,
				c.at, d.Kind, d.File.Path, l, c.kind, c.path, c.line)
		}
	}
	cfg := vunitConfigDesign["a_cfg.vhd"]
	for _, name := range []string{"v_top", "v_comp"} {
		ref, ok := r.RefAt("a_cfg.vhd", strings.Index(cfg, name))
		if !ok || ref.Decl == nil || ref.Decl.Kind != DeclVunit ||
			string(ref.Decl.Name) != name {
			t.Errorf("use vunit %s: %+v", name, ref.Decl)
		}
	}
}
