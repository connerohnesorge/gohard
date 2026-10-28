package elab

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/elab/hier"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// externals returns the external names of file p in source order.
func externals(t *testing.T, r *sema.Result, p string) []*sema.Expr {
	t.Helper()
	f, ok := r.File(p)
	if !ok {
		t.Fatalf("no file %s", p)
	}
	var out []*sema.Expr
	for n := range f.Root().Descendants() {
		if n.Kind() == cst.KindExternalName {
			out = append(out, &sema.Expr{Kind: sema.ExprName, Node: n})
		}
	}
	return out
}

func childNamed(in *hier.Instance, path string) *hier.Instance {
	for _, c := range in.Children {
		if c.Path == path {
			return c
		}
		if x := childNamed(c, path); x != nil {
			return x
		}
	}
	return nil
}

// TestExternalNamesForceExt resolves the four external names of the
// force_ext testbench (absolute, upward-relative, relative, constant)
// from the regions they are used in: all the signal names reach the
// one net of force_ext_tb.dut.mid.
func TestExternalNamesForceExt(t *testing.T) {
	r := corpus(t, "force_ext", "2008")
	d, diags := Elaborate(r, Options{Top: "force_ext_tb"})
	for _, dg := range diags {
		t.Fatalf("%s %s", dg.Code, dg.Message)
	}
	mid, ok := d.Lookup("force_ext_tb.dut.mid")
	if !ok {
		t.Fatal("no dut.mid")
	}
	chk := childNamed(d.Top, "force_ext_tb.chk")
	if chk == nil {
		t.Fatal("no block chk")
	}
	ext := externals(t, r, "force_ext_tb.vhd")
	if len(ext) != 4 {
		t.Fatalf("%d external names, want 4", len(ext))
	}
	from := []*hier.Instance{d.Top, chk, chk, chk}
	for i, e := range ext[:3] {
		x, err := d.External(from[i], e)
		if err != nil {
			t.Fatalf("%s: %v", e.Node.Text(), err)
		}
		if x.Class != sema.ClassSignal || x.Sig != mid {
			t.Errorf("%s: %+v, want signal %d", e.Node.Text(), x, mid)
		}
	}
	x, err := d.External(chk, ext[3])
	if err != nil || x.Class != sema.ClassConstant || x.Decl == nil ||
		x.Decl.Text != "C_MAGIC" || x.Inst.Path != "force_ext_tb.dut" {
		t.Errorf("C_MAGIC: %+v %v", x, err)
	}
	// The same relative names from the wrong region do not resolve.
	if _, err := d.External(chk, ext[0]); !errors.Is(err, ErrExternal) {
		t.Errorf("dut.mid from chk: %v", err)
	}
	if _, err := d.External(d.Top, ext[2]); !errors.Is(err,
		ErrExternal) {
		t.Errorf("^.dut.mid from the top: %v", err)
	}
}

const extDesign = `library ieee;
use ieee.std_logic_1164.all;

entity leaf is
  generic (W : natural := 1);
end entity;

architecture rtl of leaf is
  signal r : std_logic_vector(W - 1 downto 0) := (others => '0');
  constant K : natural := W * 10;
begin
end architecture;

entity tb is
end entity;

architecture sim of tb is
  signal top_s : bit;
begin
  g : for i in 0 to 2 generate
    u : entity work.leaf generic map (W => i + 1);
  end generate;
  c : if true generate
  begin
    inner : block is
    begin
      p : process is
        alias a is <<signal .tb.g(1).u.r : bit_vector(1 downto 0)>>;
        alias b is <<signal ^.^.g(2).u.r : bit_vector(2 downto 0)>>;
        alias k is <<constant .tb.g(2).u.K : natural>>;
        alias s is <<signal ^.^.top_s : bit>>;
        alias bad1 is <<signal .tb.g(7).u.r : bit>>;
        alias bad2 is <<constant .tb.g(1).u.r : bit>>;
        alias bad3 is <<signal .other.g(1).u.r : bit>>;
        alias bad4 is <<signal .tb.g(1).u.nope : bit>>;
      begin
        wait;
      end process;
    end block;
  end generate;
end architecture;
`

// TestExternalGenerate resolves for-generate indexes, ^ through an
// if-generate and a block, constants of a generate iteration, and the
// errors: a missing iteration, a wrong class, a wrong top and a
// missing object.
func TestExternalGenerate(t *testing.T) {
	r := analyze(t, extDesign)
	d, diags := Elaborate(r, Options{Top: "tb"})
	for _, dg := range diags {
		t.Fatalf("%s %s", dg.Code, dg.Message)
	}
	var inner *hier.Instance
	for _, s := range d.Model.Scopes {
		if strings.HasSuffix(s.Path, ".inner") {
			inner = childNamed(d.Top, s.Path)
		}
	}
	if inner == nil {
		t.Fatalf("no block inner in %v", d.Model.Scopes)
	}
	ext := externals(t, r, "d.vhd")
	want := []string{"tb.g(1).u.r", "tb.g(2).u.r", "", "tb.top_s"}
	for i, w := range want {
		x, err := d.External(inner, ext[i])
		if err != nil {
			t.Fatalf("%s: %v", ext[i].Node.Text(), err)
		}
		if w == "" {
			if x.Decl.Text != "K" || x.Inst.Path != "tb.g(2).u" {
				t.Errorf("K: %+v", x)
			}
			continue
		}
		if got := d.Model.Signals[x.Sig].Path; got != w {
			t.Errorf("%s: %s, want %s", ext[i].Node.Text(), got, w)
		}
	}
	for _, e := range ext[4:] {
		_, err := d.External(inner, e)
		if !errors.Is(err, ErrExternal) {
			t.Errorf("%s resolved: %v", e.Node.Text(), err)
			continue
		}
		// One line, the path named once.
		if m := err.Error(); strings.Contains(m, "\n") ||
			strings.Count(m, "external name") != 1 ||
			strings.Contains(m, ". in ") {
			t.Errorf("error text %q", m)
		}
	}
}

func standinResult(t *testing.T, model string) *sema.Result {
	t.Helper()
	dir := "../../testdata/corpus/standin_model"
	var srcs []sema.Source
	for _, n := range []string{"crypt_core.vhd", "standin_model.vhd",
		"standin_model_tb.vhd", "models/crypt_core_model.vhd"} {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		if n == "models/crypt_core_model.vhd" && model != "" {
			b = []byte(model)
		}
		srcs = append(srcs, sema.Source{Path: n, Text: b})
	}
	cfg := config.Defaults()
	cfg.AllowOpaque = []string{"work.crypt_core"}
	cfg.Simulation.Models = []config.Model{{Unit: "work.crypt_core",
		Path: "models/crypt_core_model.vhd"}}
	return sema.Analyze(sema.Input{Files: srcs, Config: cfg})
}

// TestStandInModeled checks the standin_model design: the instance of
// the opaque crypt_core runs the stand-in, its processes are modeled,
// everything they drive is modeled in the trace, and nothing else is.
func TestStandInModeled(t *testing.T) {
	r := standinResult(t, "")
	d, diags := Elaborate(r, Options{Top: "standin_model_tb",
		Code: func(*Design, *Proc) (sim.Code, error) {
			return inert{}, nil
		}})
	for _, dg := range diags {
		t.Fatalf("%s %s", dg.Code, dg.Message)
	}
	nmod := 0
	for _, p := range d.Model.Processes {
		in := strings.Contains(p.Path, ".u_core.") ||
			strings.Contains(p.Path, ".core.")
		if p.Modeled {
			nmod++
		}
		if p.Modeled && !in {
			t.Errorf("process %s modeled", p.Path)
		}
	}
	if nmod == 0 {
		t.Fatalf("no modeled process in %v", d.Model.Processes)
	}
	for _, s := range d.Model.Signals {
		if !s.Modeled {
			continue
		}
		base := s.Path[strings.LastIndexByte(s.Path, '.')+1:]
		if base != "dout" && base != "valid" && !strings.Contains(
			s.Path, "core") {
			t.Errorf("signal %s modeled", s.Path)
		}
	}
	// The kernel run writes a trace whose stand-in changes validate
	// (trace.Validate requires modeled processes' changes marked).
	rec := trace.NewRecorder(nil)
	k, err := sim.New(d.Model, sim.Options{Writer: rec})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(100, nil)
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

// TestStandInPorts checks a stand-in whose ports differ from the
// original's gives sim/model-ports naming the difference.
func TestStandInPorts(t *testing.T) {
	base, err := os.ReadFile(
		"../../testdata/corpus/standin_model/models/crypt_core_model.vhd")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ from, to, want string }{
		{"din   : in  std_logic_vector(7 downto 0)",
			"din   : in  std_logic_vector(6 downto 0)", "port din"},
		{"valid : out std_logic", "valid : inout std_logic",
			"port valid has mode"},
		{"valid : out std_logic", "vld : out std_logic", "port 5 is vld"},
	} {
		src := strings.Replace(string(base), c.from, c.to, 1)
		src = strings.ReplaceAll(src, "valid <=", map[bool]string{
			true: "vld <=", false: "valid <="}[c.to ==
			"vld : out std_logic"])
		r := standinResult(t, src)
		_, diags := Elaborate(r, Options{Top: "standin_model_tb"})
		found := false
		for _, dg := range diags {
			if dg.Code == diag.SimModelPorts &&
				strings.Contains(dg.Message, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: diagnostics %v", c.to, diags)
		}
	}
}
