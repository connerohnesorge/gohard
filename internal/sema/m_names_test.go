package sema

import (
	"strings"
	"testing"
)

// TestMNameAttrPrefix: the prefix of 'SIMPLE_NAME, 'PATH_NAME and
// 'INSTANCE_NAME (LRM 16.2.5) is a name of the named entity itself,
// whatever its kind: an object (an alias as written, LRM 6.6.1, and an
// access variable not dereferenced), a label, an entity, an
// architecture, a subprogram, a package, a type. The result is a
// STRING. The simulator names the declaration (elab/interp_names.go).
func TestMNameAttrPrefix(t *testing.T) {
	src := `
package pk is
  constant c : integer := 1;
end package;
entity e is end entity;
architecture a of e is
  type acc is access integer;
  signal s : bit;
  alias al is s;
  function f(x : integer) return integer is begin return x; end;
begin
  pr: process
    variable p : acc;
  begin
    report s'path_name & al'path_name & p'path_name & pr'path_name &
      e'instance_name & a'path_name & f'path_name & acc'path_name &
      work.pk'path_name & work.pk.c'simple_name;
    wait;
  end process;
end architecture;
`
	r := hAnalyze(t, "2019", map[string]string{"t.vhd": src})
	for _, d := range r.Diagnostics {
		t.Errorf("%s:%d: %s %s", d.Path, d.Line, d.Code, d.Message)
	}
	var got []string
	var kinds []DeclKind
	for _, k := range []Key{"path_name", "instance_name", "simple_name"} {
		for _, e := range hAttrs(r, k) {
			if !strings.EqualFold(e.Type.Name(), "string") {
				t.Errorf("%s: type %s, want STRING", e.Node.Text(),
					e.Type.Name())
			}
			if e.Prefix == nil || e.Prefix.Kind != ExprName ||
				e.Prefix.Decl == nil {
				t.Errorf("%s: prefix %+v, want a name of the entity",
					e.Node.Text(), e.Prefix)
				continue
			}
			got = append(got, string(e.Prefix.Decl.Name))
			kinds = append(kinds, e.Prefix.Decl.Kind)
		}
	}
	want := []Key{"s", "al", "p", "pr", "a", "f", "acc", "pk", "e", "c"}
	wantKinds := []DeclKind{DeclSignal, DeclAlias, DeclVariable,
		DeclLabel, DeclArchitecture, DeclFunction, DeclType, DeclPackage,
		DeclEntity, DeclConstant}
	if len(got) != len(want) {
		t.Fatalf("prefixes %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != string(want[i]) || kinds[i] != wantKinds[i] {
			t.Errorf("prefix %d: %s (%v), want %s (%v)", i, got[i],
				kinds[i], want[i], wantKinds[i])
		}
	}
}
