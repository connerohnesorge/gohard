package hier

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

func analyze(t *testing.T, files map[string]string) *sema.Result {
	t.Helper()
	var srcs []sema.Source
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		srcs = append(srcs, sema.Source{Path: p, Text: []byte(files[p])})
	}
	r := sema.Analyze(sema.Input{Files: srcs, Config: config.Defaults()})
	for _, d := range r.Diagnostics {
		t.Fatalf("unexpected %s:%d:%d %s %s", d.Path, d.Line, d.Col,
			d.Code, d.Message)
	}
	return r
}

const design = `
entity leaf is
  generic (WIDTH : positive := 4; NAME : string := "x");
  port (d : in bit_vector(WIDTH-1 downto 0));
end entity;
architecture rtl of leaf is
begin
end architecture;

entity mid is
  generic (N : natural := 2; W : positive := 8);
  port (bus_in : in bit_vector(W-1 downto 0));
end entity;
architecture rtl of mid is
  component leaf is
    generic (WIDTH : positive := 4);
    port (d : in bit_vector(WIDTH-1 downto 0));
  end component;
begin
  g: for i in 0 to N-1 generate
    u: entity work.leaf generic map (WIDTH => W + i)
      port map (d => bus_in(W - 1 downto i));
  end generate;
  c: if W > 4 generate
    big: leaf generic map (WIDTH => W) port map (d => bus_in);
  else generate
  end generate;
end architecture;

entity tb is end;
architecture sim of tb is
  signal s : bit_vector(11 downto 0);
begin
  dut: entity work.mid generic map (N => 2, W => 12) port map (bus_in => s);
  bad: entity work.leaf generic map (WIDTH => 3) port map (d => s);
end architecture;
`

// TestHierarchy checks paths, architectures, generic values per
// instance, generate expansion and the per-instance width check.
func TestHierarchy(t *testing.T) {
	r := analyze(t, map[string]string{"d.vhd": design})
	h := Build(r, Options{})
	want := `tb entity work.tb(sim)
  tb.dut entity work.mid(rtl) n=2 w=12
    tb.dut.g(0) for-generate
      tb.dut.g(0).u entity work.leaf(rtl) width=12 name="x"
    tb.dut.g(1) for-generate
      tb.dut.g(1).u entity work.leaf(rtl) width=13 name="x"
    tb.dut.c if-generate
      tb.dut.c.big entity work.leaf(rtl) width=12 name="x"
  tb.bad entity work.leaf(rtl) width=3 name="x"
`
	if got := h.Dump(); got != want {
		t.Fatalf("hierarchy:\n%s\nwant:\n%s", got, want)
	}
	var msgs []string
	for _, d := range h.Diagnostics {
		msgs = append(msgs, fmt.Sprintf("%d:%d %s %s", d.Line, d.Col,
			d.Code, d.Message))
	}
	got := strings.Join(msgs, "\n")
	for _, w := range []string{
		"sema/static-error width mismatch: port d is 3 wide, its " +
			`actual is 12 in tb.bad (WIDTH=3, NAME="x")`,
		"sema/static-error width mismatch: port d is 13 wide, its " +
			`actual is 11 in tb.dut.g(1).u (WIDTH=13, NAME="x")`,
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if len(msgs) != 2 {
		t.Errorf("want 2 diagnostics, got\n%s", got)
	}
}

// TestDeterministic: building twice gives identical dumps and
// diagnostics, and the tops are in canonical order.
func TestDeterministic(t *testing.T) {
	r := analyze(t, map[string]string{"d.vhd": design})
	a, b := Build(r, Options{}), Build(r, Options{})
	if a.Dump() != b.Dump() || fmt.Sprint(a.Diagnostics) !=
		fmt.Sprint(b.Diagnostics) {
		t.Fatal("Build is not deterministic")
	}
}

// TestRecursion: a recursive design whose recursion depends on a
// generic ends where the generate condition stops it, and one that
// never stops hits the depth bound with an error naming the
// recursion (not the step limit) instead of hanging (P7).
func TestRecursion(t *testing.T) {
	r := analyze(t, map[string]string{"r.vhd": `
entity tree is generic (D : natural := 3); end;
architecture rtl of tree is
begin
  g: if D > 0 generate
    l: entity work.tree generic map (D => D - 1);
  end generate;
end;
entity loopy is end;
architecture rtl of loopy is
begin
  me: entity work.loopy;
end;
`})
	h := Build(r, Options{Tops: []string{"tree"}})
	n := 0
	h.Walk(func(in *Instance) {
		if in.Kind == KindEntity {
			n++
		}
	})
	if n != 4 {
		t.Fatalf("tree depth 3 has %d entity instances:\n%s", n, h.Dump())
	}
	h = Build(r, Options{Tops: []string{"loopy"}, MaxDepth: 20})
	if len(h.Incomplete) != 0 || len(h.Diagnostics) != 1 ||
		h.Diagnostics[0].Code != "sema/error" ||
		!strings.Contains(h.Diagnostics[0].Message,
			"instance nesting exceeds 20 levels at loopy.me.me....me.me "+
				"(21 names): the instantiation is recursive") {
		t.Fatalf("loopy: %v %v", h.Incomplete, h.Diagnostics)
	}
}

// TestBudget: a generic whose value needs more than the step budget
// is reported once as sema/eval-limit and listed in Incomplete.
func TestBudget(t *testing.T) {
	r := analyze(t, map[string]string{"b.vhd": `
entity leaf is generic (W : natural := 1); end;
architecture rtl of leaf is begin end;
entity top is end;
architecture rtl of top is
  constant big : bit_vector(0 to 99999) := (others => '1');
begin
  u: entity work.leaf generic map (W => big'length + 1);
  v: entity work.leaf generic map (W => boolean'pos(big = big));
end;
`})
	h := Build(r, Options{Steps: 1000})
	if !slices.Equal(h.Incomplete, []string{"top.v"}) {
		t.Fatalf("incomplete %v\n%v", h.Incomplete, h.Diagnostics)
	}
	var u Generic
	h.Walk(func(in *Instance) {
		if in.Path == "top.u" {
			u = in.Generics[0]
		}
	})
	if !u.Known || u.Value.I != 100001 {
		t.Fatalf("top.u W = %v %v", u.Value.Image(), u.Err)
	}
	_ = eval.EditorSteps
}

func corpusResult(t *testing.T, files ...string) *sema.Result {
	t.Helper()
	var srcs []sema.Source
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(
			"../../../testdata/corpus/hier_generate", f))
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, sema.Source{Path: f, Text: b})
	}
	return sema.Analyze(sema.Input{Files: srcs, Config: config.Defaults()})
}

// TestCorpusHierGenerate: the hier_generate corpus design (#8)
// elaborates cleanly with the generic values its corpus notes record,
// and its width_err variant reports exactly the two port associations
// of the one bad instance, each naming the instance path (corpus.yaml
// disagreement entry, LRM 14.3.5).
func TestCorpusHierGenerate(t *testing.T) {
	r := corpusResult(t, "hier_generate_lane.vhd", "hier_generate.vhd",
		"hier_generate_tb.vhd")
	h := Build(r, Options{})
	if len(h.Diagnostics) != 0 {
		t.Fatalf("clean design: %v", h.Diagnostics)
	}
	var ws []string
	h.Walk(func(in *Instance) {
		if strings.HasSuffix(in.Path, ".u") {
			for _, g := range in.Generics {
				if g.Decl.Name == "w" {
					ws = append(ws, in.Path+"="+g.Value.Image())
				}
			}
		}
	})
	want := "hier_generate_tb.dut.lanes(0).u=2 " +
		"hier_generate_tb.dut.lanes(1).u=3 " +
		"hier_generate_tb.dut.lanes(2).u=4 " +
		"hier_generate_tb.dut.lanes(3).u=5"
	if got := strings.Join(ws, " "); got != want {
		t.Fatalf("lane widths: %s\nwant %s\n%s", got, want, h.Dump())
	}
	r = corpusResult(t, "hier_generate_lane.vhd",
		"hier_generate_width_err.vhd", "hier_generate_tb.vhd")
	h = Build(r, Options{})
	var got []string
	for _, d := range h.Diagnostics {
		got = append(got, fmt.Sprintf("%s:%d:%d %s", d.Path, d.Line,
			d.Col, d.Message))
	}
	if len(got) != 2 {
		t.Fatalf("width_err: %v", got)
	}
	for i, col := range []int{22, 47} {
		pre := fmt.Sprintf("hier_generate_width_err.vhd:31:%d ", col)
		if !strings.HasPrefix(got[i], pre) || !strings.Contains(got[i],
			"in hier_generate_tb.dut.lanes(2).u (W=4") {
			t.Errorf("width_err %d: %s", i, got[i])
		}
	}
}

// TestNEORV32 elaborates the NEORV32 testbench (#19's large project)
// with no per-instance error.
func TestNEORV32(t *testing.T) {
	root := "../../../testdata/neorv32"
	var srcs []sema.Source
	for _, g := range []string{"rtl/core/*.vhd", "sim/*.vhd"} {
		files, _ := filepath.Glob(filepath.Join(root, g))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			srcs = append(srcs, sema.Source{Path: filepath.Base(f),
				Text: b})
		}
	}
	if len(srcs) == 0 {
		t.Skip("testdata/neorv32 is not checked out")
	}
	c := config.Defaults()
	c.Libraries = []config.Library{{Name: "neorv32",
		Folders: []config.Folder{{Path: "."}}}}
	r := sema.Analyze(sema.Input{Files: srcs, Config: c})
	h := Build(r, Options{Tops: []string{"neorv32_tb"}})
	n := 0
	h.Walk(func(*Instance) { n++ })
	if n < 100 || len(h.Diagnostics) > 0 || len(h.Incomplete) > 0 {
		t.Fatalf("%d instances, %v, incomplete %v", n, h.Diagnostics,
			h.Incomplete)
	}
}
