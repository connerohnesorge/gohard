package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
)

// TestUncheckedQuiet: a unit that depends on an opaque unit reports
// only that it was not checked: a name it reads from the opaque unit
// (which GoHard cannot see into) is no error of its own. Its syntax
// errors still are.
func TestUncheckedQuiet(t *testing.T) {
	// A stand-in unimplemented construct (group templates) makes p
	// opaque, whatever GoHard leaves unimplemented.
	saved := constructs
	constructs = append(constructs[:len(constructs):len(constructs)],
		construct{kind: cst.KindGroupTemplateDecl,
			name: "group templates"})
	defer func() { constructs = saved }()
	files := map[string]string{
		"p.vhd": `package p is
  group pair is (signal, signal);
end;
`,
		"q.vhd": `use work.p.all;
package q is
  constant k : integer := f(1);
end;
`,
		"r.vhd": `use work.p.all;
package r is
  constant k : integer := 1
end;
`,
	}
	r := analyze(t, files, func(c *config.Config) { c.Standard = "2019" })
	got := strings.TrimSpace(diagText(r))
	want := []string{
		"p.vhd:2:3: unsupported/construct",
		"q.vhd:2:9: sema/unchecked-unit",
		"r.vhd:3:28: syntax/",
		"r.vhd:2:9: sema/unchecked-unit",
	}
	lines := strings.Split(got, "\n")
	if len(lines) != len(want) {
		t.Fatalf("got\n%s", got)
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %s in\n%s", w, got)
		}
	}
}
