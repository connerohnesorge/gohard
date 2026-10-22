package hier

import (
	"strings"
	"testing"
	"testing/quick"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

const namesDesign = `
entity leaf is
  generic (W : positive := 4; NAME : string := "x");
end entity;
architecture rtl of leaf is
begin
end architecture;

entity mid is
  generic (N : natural := 2);
end entity;
architecture rtl of mid is
  type color is (red, green, blue);
  component leaf is
    generic (W : positive := 4; NAME : string := "x");
  end component;
begin
  g: for i in 0 to N-1 generate
    u: entity work.leaf generic map (W => 4 + i);
  end generate;
  e: for c in green to blue generate
    b: block begin end block;
  end generate;
  c: if alt: N > 1 generate
    big: leaf generic map (W => 8);
  else generate
  end generate;
  k: case N generate
    when 2 => kb: block begin end block;
    when others =>
  end generate;
end architecture;

entity tb is end;
architecture sim of tb is
begin
  dut: entity work.mid generic map (N => 2);
  named: entity work.leaf
    generic map (NAME => named'path_name & "|" & tb'instance_name &
      "|" & sim'path_name);
end architecture;
`

func namesOf(t *testing.T) *Result {
	t.Helper()
	res := Build(analyze(t, map[string]string{"d.vhd": namesDesign}),
		Options{Tops: []string{"tb"}})
	if len(res.Tops) != 1 {
		t.Fatalf("tops %d, want 1", len(res.Tops))
	}
	return res
}

// TestMNamesInstances: the NVC 1.23 forms of the instance names (the
// prefix of every 'PATH_NAME and 'INSTANCE_NAME inside).
func TestMNamesInstances(t *testing.T) {
	res := namesOf(t)
	got := map[string]Names{}
	res.Walk(func(in *Instance) { got[in.Path] = in.Names() })
	want := map[string]Names{
		"tb":          {":tb", ":tb(sim)"},
		"tb.dut":      {":tb:dut", ":tb(sim):dut@mid(rtl)"},
		"tb.dut.g(0)": {":tb:dut:g(0)", ":tb(sim):dut@mid(rtl):g(0)"},
		"tb.dut.g(1).u": {
			":tb:dut:g(1):u",
			":tb(sim):dut@mid(rtl):g(1):u@leaf(rtl)",
		},
		"tb.dut.e(green)": {":tb:dut:e(1)", ":tb(sim):dut@mid(rtl):e(1)"},
		"tb.dut.e(blue).b": {":tb:dut:e(2):b",
			":tb(sim):dut@mid(rtl):e(2):b"},
		"tb.dut.c.alt": {":tb:dut:alt", ":tb(sim):dut@mid(rtl):alt"},
		"tb.dut.c.alt.big": {
			":tb:dut:alt:big",
			":tb(sim):dut@mid(rtl):alt:big@leaf(rtl)",
		},
		"tb.dut.k":    {":tb:dut:k", ":tb(sim):dut@mid(rtl):k"},
		"tb.dut.k.kb": {":tb:dut:k:kb", ":tb(sim):dut@mid(rtl):k:kb"},
		"tb.named":    {":tb:named", ":tb(sim):named@leaf(rtl)"},
	}
	for p, w := range want {
		if g, ok := got[p]; !ok || g != w {
			t.Errorf("%s: %+v, want %+v (have %v)", p, g, w, ok)
		}
	}
}

// TestMNamesProperties: for every instance, the names extend the
// parent's by one ':' element, are lower case and unique, and the
// instance name without its "(arch)" and "@entity(arch)" parts is the
// path name.
func TestMNamesProperties(t *testing.T) {
	res := namesOf(t)
	seen := map[string]bool{}
	res.Walk(func(in *Instance) {
		n := in.Names()
		if n.Path != Lower(n.Path) || n.Inst != Lower(n.Inst) {
			t.Errorf("%s: not lower case: %+v", in.Path, n)
		}
		if seen[n.Path] {
			t.Errorf("%s: path name %s twice", in.Path, n.Path)
		}
		seen[n.Path] = true
		if stripInst(n.Inst) != n.Path {
			t.Errorf("%s: %s stripped is %s, want %s", in.Path, n.Inst,
				stripInst(n.Inst), n.Path)
		}
		for _, c := range in.Children {
			cn := c.Names()
			if !strings.HasPrefix(cn.Path, n.Path+":") ||
				!strings.HasPrefix(cn.Inst, n.Inst+":") {
				t.Errorf("%s: child %+v does not extend %+v", c.Path,
					cn, n)
			}
			rest := strings.TrimPrefix(cn.Path, n.Path+":")
			if rest == "" || strings.Contains(rest, ":") {
				t.Errorf("%s: child adds %q, want one element", c.Path,
					rest)
			}
			if c.Parent() != in {
				t.Errorf("%s: Parent is not %s", c.Path, in.Path)
			}
		}
	})
}

// stripInst drops the architecture of the top and every
// "@entity(arch)" of an instance name.
func stripInst(s string) string {
	parts := strings.Split(s, ":")
	for i, p := range parts {
		if i == 1 {
			if j := strings.IndexByte(p, '('); j >= 0 {
				p = p[:j]
			}
		}
		if j := strings.IndexByte(p, '@'); j >= 0 {
			p = p[:j]
		}
		parts[i] = p
	}
	return strings.Join(parts, ":")
}

// TestMNamesGenericActual: a name attribute in a generic actual is
// evaluated as code of the instantiating instance: an instance label
// is a leaf (":tb:named"; NVC 1.23 is not consistent here), an entity
// and an architecture end with ':'.
func TestMNamesGenericActual(t *testing.T) {
	res := namesOf(t)
	var named *Instance
	res.Walk(func(in *Instance) {
		if in.Path == "tb.named" {
			named = in
		}
	})
	if named == nil {
		t.Fatal("no tb.named")
	}
	var g *Generic
	for i := range named.Generics {
		if named.Generics[i].Decl.Name == "name" {
			g = &named.Generics[i]
		}
	}
	if g == nil || !g.Known {
		t.Fatalf("NAME not known: %+v", g)
	}
	if got, want := strOf(g.Value), ":tb:named|:tb(sim):|:tb:"; got != want {
		t.Errorf("NAME = %q, want %q", got, want)
	}
}

func strOf(v eval.Value) string {
	var b strings.Builder
	for _, e := range v.E {
		b.WriteByte(byte(e.I))
	}
	return b.String()
}

// TestMNamesProfile: subprogram profiles name the parameter and result
// subtypes in lower case, without spaces between parameters.
func TestMNamesProfile(t *testing.T) {
	r := analyze(t, map[string]string{"p.vhd": `
package p is
  type Rec is record a : integer; end record;
  procedure Proc(a : natural; b : bit_vector; signal s : in bit);
  procedure none;
  function F(x : Rec) return String;
  function g return boolean;
end package;
`})
	want := map[sema.Key]string{
		"proc": "proc[natural,bit_vector,bit]",
		"none": "none[]",
		"f":    "f[rec return string]",
		"g":    "g[return boolean]",
	}
	for _, u := range r.Units {
		if u.Region == nil || u.Name != "p" {
			continue
		}
		for _, d := range u.Region.Decls {
			if w, ok := want[d.Name]; ok {
				if got := Profile(d); got != w {
					t.Errorf("Profile(%s) = %q, want %q", d.Name, got, w)
				}
				delete(want, d.Name)
			}
		}
	}
	if len(want) > 0 {
		t.Errorf("not found: %v", want)
	}
}

// TestMNamesLower: Lower is idempotent, keeps the length and leaves no
// upper-case ASCII letter.
func TestMNamesLower(t *testing.T) {
	f := func(s string) bool {
		l := Lower(s)
		if len(l) != len(s) || Lower(l) != l {
			return false
		}
		for i := range len(l) {
			if c := l[i]; c >= 'A' && c <= 'Z' {
				return false
			}
			if s[i] < 'A' || s[i] > 'Z' {
				if l[i] != s[i] {
					return false
				}
			}
		}
		return true
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
	if got := Lower(`\Ext Sig\`); got != `\ext sig\` {
		t.Errorf("Lower = %q", got)
	}
}
