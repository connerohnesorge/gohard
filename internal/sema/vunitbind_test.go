package sema

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cst"
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

// vunitIncrementalDesign binds unbound verification units with use
// vunit in component configurations without an entity aspect
// (incremental bindings, LRM 7.3.2.1, #66): the configuration
// specifications of tb bind u1 to dut(rtl) and, in the generate g, u2
// to dut, whose architecture the component configuration's block
// configuration names. The component is dut_c, so the default binding
// (an entity dut_c) is not the one that applies.
var vunitIncrementalDesign = map[string]string{
	"a_cfg.vhd": `configuration cfg of tb is
  for sim
    for u1 : dut_c
      use vunit v_one;
    end for;
    for g
      for all : dut_c
        use vunit v_two;
        for rtl
        end for;
      end for;
    end for;
  end for;
end configuration;
`,
	"b_props.vhd": `vunit v_one {
  default clock is rising_edge(clk);
  assert always (x -> y);
}
vunit v_two {
  default clock is rising_edge(clk);
  assert always (q -> width = 4);
}
`,
	"c_tb.vhd": `library ieee;
use ieee.std_logic_1164.all;
entity tb is end entity;
architecture sim of tb is
  signal clk, a, b : std_logic := '0';
  component dut_c is
    port (clk : in std_logic; x, y : in std_logic);
  end component;
  for u1 : dut_c use entity work.dut(rtl);
begin
  u1 : dut_c port map (clk, a, b);
  g : if true generate
    for others : dut_c use entity work.dut;
  begin
    u2 : dut_c port map (clk, b, a);
  end generate;
end architecture;
`,
	"d_dut.vhd": vunitConfigDesign["d_dut.vhd"],
}

// TestVunitIncrementalBinding (#66): a use vunit in a component
// configuration without an entity aspect binds the units to the design
// entity of the configuration specification that binds the instances
// (u1: dut(rtl); u2, in a generate: dut with the architecture of the
// component configuration's block configuration), so their names are
// dut's ports, generic and rtl signal, and the run of check has no
// error.
func TestVunitIncrementalBinding(t *testing.T) {
	r := analyze(t, vunitIncrementalDesign, nil)
	markedFileErrors(t, r, vunitIncrementalDesign)
	src := vunitIncrementalDesign["b_props.vhd"]
	for _, c := range []struct {
		name string
		kind DeclKind
		line int
	}{
		{"x -> y", DeclPort, 5},
		{"q ->", DeclSignal, 8},
		{"width", DeclGeneric, 4},
	} {
		ref, ok := r.RefAt("b_props.vhd", strings.Index(src, c.name))
		if !ok || ref.Decl == nil {
			t.Errorf("%s: no reference", c.name)
			continue
		}
		d := ref.Decl
		if l, _ := d.File.Lines.LineCol(d.Span.Start); d.Kind != c.kind ||
			d.File.Path != "d_dut.vhd" || l != c.line {
			t.Errorf("%s: %s at %s:%d, want %s at d_dut.vhd:%d", c.name,
				d.Kind, d.File.Path, l, c.kind, c.line)
		}
	}
}

// vunitRecursiveDesign is a recursive design (#66): the configuration
// specification in tree(rtl)'s generate binds the nested instances to
// tree(rtl) itself and names v, so v depends on tree(rtl), which holds
// the indication naming v.
var vunitRecursiveDesign = map[string]string{
	"props.vhd": `vunit v {
  default clock is rising_edge(clk);
  assert always (a -> b);
}
`,
	"tree.vhd": `library ieee;
use ieee.std_logic_1164.all;
entity tree is
  generic (n : natural := 2);
  port (clk : in std_logic; a, b : in std_logic);
end entity;
architecture rtl of tree is
  component tree is
    generic (n : natural := 2);
    port (clk : in std_logic; a, b : in std_logic);
  end component;
begin
  g : if n > 0 generate
    for all : tree use entity work.tree(rtl);
      use vunit v;
      use vunit work.v;
    end for;
  begin
    u : tree generic map (n - 1) port map (clk, a, b);
  end generate;
end architecture;
`,
}

// TestVunitRecursiveDesign (#66): use vunit in the configuration
// specification of a recursive design is no import cycle: tree(rtl)
// does not depend on v (which depends on tree(rtl)), the names of v
// are tree's ports, and Result.NamedVunit finds v for both names of
// the indication, which have no reference.
func TestVunitRecursiveDesign(t *testing.T) {
	r := analyze(t, vunitRecursiveDesign, nil)
	markedFileErrors(t, r, vunitRecursiveDesign)
	var tree, v *Unit
	for _, u := range r.Units {
		switch {
		case u.Kind == UnitArchitecture && u.Name == "rtl":
			tree = u
		case u.Kind == UnitVunit:
			v = u
		}
	}
	if tree == nil || v == nil {
		t.Fatal("units missing")
	}
	if slices.Contains(tree.Deps, v) {
		t.Errorf("tree(rtl) depends on v")
	}
	src := vunitRecursiveDesign["props.vhd"]
	ref, ok := r.RefAt("props.vhd", strings.Index(src, "a ->"))
	if !ok || ref.Decl == nil || ref.Decl.Kind != DeclPort {
		t.Errorf("a in v: %+v", ref.Decl)
	}
	ts := vunitRecursiveDesign["tree.vhd"]
	for _, name := range []string{"vunit v;", "vunit work.v;"} {
		off := strings.Index(ts, name) + len("vunit ")
		n := tree.Node
		for x := range tree.Node.Descendants() {
			if x.Span().Start == off &&
				(x.Kind() == cst.KindSimpleName ||
					x.Kind() == cst.KindSelectedName) {
				n = x
				break
			}
		}
		if got := r.NamedVunit(tree, n); got != v {
			t.Errorf("NamedVunit %s: %v", name, got)
		}
	}
}
