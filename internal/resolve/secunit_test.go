package resolve

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// secFiles has an entity with an architecture and a configuration,
// each named from another file.
var secFiles = map[string]string{
	"b.vhd": `entity b is port (y : in bit); end entity b;
architecture rtl of b is begin end architecture rtl;
`,
	"a.vhd": `entity a is end entity a;
architecture rtl2 of a is
  signal s : bit;
begin
  u2: entity work.b(rtl) port map (y => s);
end architecture rtl2;
configuration cfg of a is for rtl2 end for; end configuration cfg;
`,
}

func secResult(t *testing.T) *sema.Result {
	t.Helper()
	var srcs []sema.Source
	for _, p := range []string{"a.vhd", "b.vhd"} {
		srcs = append(srcs, sema.Source{Path: p,
			Text: []byte(secFiles[p])})
	}
	r := sema.Analyze(sema.Input{Files: srcs, Config: config.Defaults()})
	if len(r.Diagnostics) > 0 {
		t.Fatalf("diagnostics: %v", r.Diagnostics)
	}
	return r
}

func secOff(t *testing.T, path, s string, nth int) int {
	t.Helper()
	text, from := secFiles[path], 0
	for {
		i := strings.Index(text[from:], s)
		if i < 0 {
			t.Fatalf("%s: no %q #%d", path, s, nth)
		}
		if nth == 0 {
			return from + i
		}
		nth--
		from += i + 1
	}
}

// TestRenameSecondaryUnit: an architecture's or configuration's name
// is its own symbol; renaming it never touches the entity.
func TestRenameSecondaryUnit(t *testing.T) {
	r := secResult(t)
	cases := []struct {
		path, name string
		nth        int
		want       map[string]string
	}{
		{"b.vhd", "rtl", 0, map[string]string{
			"b.vhd": "entity b is port (y : in bit); end entity b;\n" +
				"architecture nn of b is begin end architecture nn;\n",
			"a.vhd": "u2: entity work.b(nn) port map",
		}},
		{"a.vhd", "(rtl)", 0, map[string]string{
			"b.vhd": "architecture nn of b is begin end architecture nn;",
			"a.vhd": "u2: entity work.b(nn) port map",
		}},
		{"a.vhd", "cfg", 0, map[string]string{
			"a.vhd": "configuration nn of a is for rtl2 end for; " +
				"end configuration nn;",
			"b.vhd": "entity b is port (y : in bit); end entity b;",
		}},
		{"a.vhd", "rtl2", 0, map[string]string{
			"a.vhd": "architecture nn of a is",
		}},
	}
	for _, c := range cases {
		o := secOff(t, c.path, c.name, c.nth)
		if strings.HasPrefix(c.name, "(") {
			o++
		}
		edits, err := Rename(r, c.path, o, "nn")
		if err != nil {
			t.Fatalf("%s %s: %v", c.path, c.name, err)
		}
		out := apply(secFiles, edits)
		for p, w := range c.want {
			if !strings.Contains(out[p], w) {
				t.Errorf("rename %s in %s: %s is\n%s\nwant %q",
					c.name, c.path, p, out[p], w)
			}
		}
		if !strings.Contains(out["b.vhd"], "entity b is") ||
			!strings.Contains(out["a.vhd"], "entity a is") ||
			!strings.Contains(out["a.vhd"], "work.b(") {
			t.Errorf("rename %s touched an entity:\n%s%s", c.name,
				out["a.vhd"], out["b.vhd"])
		}
		// References agree with the rename edits.
		refs := References(r, c.path, o, true)
		if len(refs) != len(edits) {
			t.Errorf("%s: %d references, %d edits", c.name,
				len(refs), len(edits))
		}
	}
}
