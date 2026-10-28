package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// corpusResult analyzes the files of the oracle corpus design name.
func corpusResult(t *testing.T, name string) *sema.Result {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "corpus", name)
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var srcs []sema.Source
	for _, e := range es {
		if !strings.HasSuffix(e.Name(), ".vhd") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, sema.Source{Path: e.Name(), Text: b})
	}
	r := sema.Analyze(sema.Input{Files: srcs, Config: config.Defaults()})
	if len(r.Diagnostics) > 0 {
		t.Fatalf("diagnostics: %v", r.Diagnostics)
	}
	return r
}

// TestSignatureHelpAgreesWithHover: on the overload_ops corpus design
// (#8), at every offset where signature help answers, its first
// signature is the overload hover and go to definition give at the
// call's name, and every call written with an association list or as
// an operator gets signature help somewhere (#56).
func TestSignatureHelpAgreesWithHover(t *testing.T) {
	r := corpusResult(t, "overload_ops")
	paths := []string{"overload_ops.vhd", "overload_ops_pkg.vhd",
		"overload_ops_tb.vhd"}
	checked := 0
	for _, p := range paths {
		f, ok := r.File(p)
		if !ok {
			t.Fatalf("no file %s", p)
		}
		want := map[*sema.Expr]bool{}
		eachExpr(f, func(e *sema.Expr) {
			if _, ok := callList(e); ok && e.Overload != nil {
				want[e] = true
			}
			// An operator inside a call's association list gets
			// the call's signature help, not its own.
			if _, _, inCall := innermostCall(f,
				e.Node.Span().Start); !inCall && e.Overload != nil &&
				e.Kind == sema.ExprCall &&
				(e.Node.Kind() == cst.KindBinaryExpr ||
					e.Node.Kind() == cst.KindUnaryExpr) {
				want[e] = true
			}
		})
		seen := map[*sema.Expr]bool{}
		for off := 0; off <= len(f.Text); off++ {
			sigs, ok := SignatureHelp(r, p, off)
			if !ok {
				continue
			}
			e, _, isCall := innermostCall(f, off)
			if m, isMap := innermostMap(f, off); isMap && (!isCall ||
				!within(innermostList(f, off), m.list.Span())) {
				continue // a map: no overloads
			}
			if !isCall {
				e, _ = innermostOperator(f, off)
			}
			seen[e] = true
			ref, ok := callRef(f, e)
			if !ok {
				t.Fatalf("%s:%d: no reference at the call's name", p, off)
			}
			at := fmt.Sprintf("%s:%d (%s)", p, off,
				f.Text[ref.Span.Start:ref.Span.End])
			if ref.Decl == nil {
				t.Errorf("%s: the call does not resolve", at)
				continue
			}
			if sigs[0].Decl != ref.Decl {
				t.Errorf("%s: signature help %s, reference %s", at,
					sema.Describe(sigs[0].Decl), sema.Describe(ref.Decl))
			}
			h, ok := HoverAt(r, p, ref.Span.Start)
			if !ok || h.Decl != sigs[0].Decl {
				t.Errorf("%s: hover %v, signature help %s", at, h.Decl,
					sema.Describe(sigs[0].Decl))
			}
			l, _ := LocationOf(sigs[0].Decl)
			if d := Definition(r, p, ref.Span.Start); len(d) != 1 ||
				d[0] != l {
				t.Errorf("%s: definition %s, signature help %s", at,
					locs(d), locs([]Location{l}))
			}
			for i, s := range sigs {
				if i > 0 && s.Decl == sigs[0].Decl {
					t.Errorf("%s: the resolved overload is listed twice",
						at)
				}
			}
			checked++
		}
		for e := range want {
			if !seen[e] {
				sp := e.Node.Span()
				t.Errorf("%s:%d: the call %q gets no signature help", p,
					sp.Start, f.Text[sp.Start:sp.End])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no call checked")
	}
	t.Logf("%d positions checked", checked)
}

// innermostList is the association list of the innermost call at off.
func innermostList(f *sema.File, off int) cst.Range {
	_, l, ok := innermostCall(f, off)
	if !ok {
		return cst.Range{}
	}
	return l.Span()
}

func TestSignatureHelpActiveParameter(t *testing.T) {
	r := analyzeOne(t, `entity e is
  generic (W : natural := 8; D : natural := 2);
  port (a : in bit; b : out bit; c : in bit);
end entity;
architecture rtl of e is
  function f(x : integer; y : integer; z : integer) return integer is
  begin
    return x;
  end function;
  signal s, t : bit;
  constant k1 : integer := f(1, 2, 3);
  constant k2 : integer := f(z => 1, x => 2, y => 3);
  constant k3 : integer := f(1, z => 2, y => 3);
begin
  u0 : entity work.e generic map (D => 3) port map (s, t, c => s);
end architecture;
`)
	src := string(mustFile(t, r, "a.vhd").Text)
	at := func(s string, n int) int {
		i := strings.Index(src, s)
		if i < 0 {
			t.Fatalf("no %q", s)
		}
		return i + n
	}
	for _, c := range []struct {
		name  string
		off   int
		label string
		param int
	}{
		{"first positional", at("f(1, 2, 3)", 2), "function f", 0},
		{"after a comma", at("f(1, 2, 3)", 5), "function f", 1},
		{"third positional", at("f(1, 2, 3)", 8), "function f", 2},
		{"named z", at("f(z =>", 2), "function f", 2},
		{"named x", at("x => 2", 1), "function f", 0},
		{"positional then named", at("f(1, z", 2), "function f", 0},
		{"named after positional", at("y => 3);\nbegin", 0),
			"function f", 1},
		{"generic map, named", at("D => 3", 0), "entity e generic", 1},
		{"port map, positional", at("(s, t", 4), "entity e port", 1},
		{"port map, named", at("c => s", 0), "entity e port", 2},
	} {
		sigs, ok := SignatureHelp(r, "a.vhd", c.off)
		if !ok {
			t.Errorf("%s: no signature help", c.name)
			continue
		}
		if !strings.HasPrefix(sigs[0].Label, c.label) ||
			sigs[0].Active != c.param {
			t.Errorf("%s: %q active %d, want %q... active %d", c.name,
				sigs[0].Label, sigs[0].Active, c.label, c.param)
		}
		for i, p := range sigs[0].Params {
			if p.Start < 0 || p.End > len(sigs[0].Label) ||
				p.Start >= p.End {
				t.Errorf("%s: parameter %d at %v in %q", c.name, i, p,
					sigs[0].Label)
			}
		}
	}
	if _, ok := SignatureHelp(r, "a.vhd", at("signal s", 0)); ok {
		t.Error("signature help outside any call")
	}
}

func mustFile(t *testing.T, r *sema.Result, p string) *sema.File {
	t.Helper()
	f, ok := r.File(p)
	if !ok {
		t.Fatalf("no file %s", p)
	}
	return f
}

func TestSignatureLabels(t *testing.T) {
	r := analyzeOne(t, `entity e is
  generic (W : natural := 8);
  port (a : in bit; signal b : out bit_vector(W - 1 downto 0));
end entity;
architecture rtl of e is
  procedure p(constant x : in integer; variable y : inout integer) is
  begin
  end procedure;
begin
  u0 : entity work.e generic map (4) port map (a => open, b => open);
  process
    variable v : integer;
  begin
    p(1, v);
    wait;
  end process;
end architecture;
`)
	src := string(mustFile(t, r, "a.vhd").Text)
	for _, c := range []struct {
		at, want string
		params   []string
	}{
		{"(4)", "entity e generic (W : natural := 8)",
			[]string{"W : natural := 8"}},
		{"(a =>", "entity e port (a : in bit; signal b : out " +
			"bit_vector(W - 1 downto 0))",
			[]string{"a : in bit", "signal b : out bit_vector(W - 1 " +
				"downto 0)"}},
		{"(1, v)", "procedure p(constant x : in integer; variable y " +
			": inout integer)", []string{"constant x : in integer",
			"variable y : inout integer"}},
	} {
		sigs, ok := SignatureHelp(r, "a.vhd", strings.Index(src, c.at)+1)
		if !ok {
			t.Fatalf("%s: no signature help", c.at)
		}
		s := sigs[0]
		var params []string
		for _, p := range s.Params {
			params = append(params, s.Label[p.Start:p.End])
		}
		if s.Label != c.want || !slices.Equal(params, c.params) {
			t.Errorf("%s: %q %q, want %q %q", c.at, s.Label, params,
				c.want, c.params)
		}
	}
}
