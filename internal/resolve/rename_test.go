package resolve

import (
	"strings"
	"testing"
	"testing/quick"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// TestKeepCase: a use site spelled like the declaration gets the new
// name as typed; an all upper-case or all lower-case site gets it in
// its case; an extended new name is never changed (#25).
func TestKeepCase(t *testing.T) {
	letters := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				b.WriteRune(r)
			}
		}
		if b.Len() == 0 {
			return "aB"
		}
		return b.String()
	}
	prop := func(site, decl, name string) bool {
		site, decl, name = letters(site), letters(decl), letters(name)
		got := keepCase(site, decl, name)
		if !strings.EqualFold(got, name) {
			return false
		}
		switch {
		case site == decl:
			return got == name
		case site == strings.ToUpper(site):
			return got == strings.ToUpper(name)
		case site == strings.ToLower(site):
			return got == strings.ToLower(name)
		}
		return got == name
	}
	if err := quick.Check(prop, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][4]string{
		{"COUNTER", "Counter", "tally", "TALLY"},
		{"counter", "Counter", "Tally", "tally"},
		{"Counter", "Counter", "Tally", "Tally"},
		{"cOUNTER", "Counter", "Tally", "Tally"},
		{"COUNTER", "Counter", `\My Tally\`, `\My Tally\`},
	} {
		if got := keepCase(c[0], c[1], c[2]); got != c[3] {
			t.Errorf("keepCase(%q, %q, %q) = %q, want %q", c[0], c[1],
				c[2], got, c[3])
		}
	}
}

// TestRenameRefusals: bundled, predefined, extended, opaque and
// stand-in declarations are not renamed, and PrepareRename agrees.
func TestRenameRefusals(t *testing.T) {
	src := `library ieee;
use ieee.std_logic_1164.all;
entity e is
end entity e;
architecture a of e is
  signal \My Sig\ : std_logic;
  type t is (x, y);
  signal s : t;
  signal b : boolean;
begin
  \My Sig\ <= '1';
  b <= s = x;
end architecture a;
`
	r := analyzeOne(t, src)
	for _, c := range []struct {
		needle string
		want   error
	}{
		{"std_logic;", ErrBundled},
		{"= x", ErrImplicit},
		{"<= '1'", ErrNotRenamable},
		{`\My Sig\ <=`, ErrExtended},
		{"ieee;", ErrNotRenamable},
	} {
		o := strings.Index(src, c.needle)
		if _, err := Rename(r, "a.vhd", o, "x"); err != c.want {
			t.Errorf("%s: %v, want %v", c.needle, err, c.want)
		}
		if _, ok := PrepareRename(r, "a.vhd", o); ok {
			t.Errorf("%s: PrepareRename accepts it", c.needle)
		}
	}
	if _, err := Rename(r, "a.vhd", strings.Index(src, "b <="),
		"c"); err != nil {
		t.Errorf("b: %v", err)
	}
}

func analyzeOne(t *testing.T, src string) *sema.Result {
	t.Helper()
	return sema.Analyze(sema.Input{Files: []sema.Source{{Path: "a.vhd",
		Text: []byte(src)}}, Config: config.Defaults()})
}

// TestRenameOpaque: a declaration of an opaque unit (one with an
// encrypted IEEE 1735 envelope inside it) is not renamed.
func TestRenameOpaque(t *testing.T) {
	src := `entity leaf is end;
architecture rtl of leaf is begin end;
entity top is end;
architecture rtl of top is
  signal pair_s : bit;
  component leaf is end component;
` + "`" + `protect begin_protected
` + "`" + `protect data_block
bm90IHJlYWwgY2lwaGVydGV4dCwgb25seSBmaWxsZXI=
` + "`" + `protect end_protected
begin
  u1 : component leaf;
  pair_s <= '1';
end;
`
	r := analyzeOne(t, src)
	o := strings.Index(src, "pair_s")
	if _, err := Rename(r, "a.vhd", o, "x"); err != ErrOpaque {
		t.Errorf("opaque: %v", err)
	}
}
