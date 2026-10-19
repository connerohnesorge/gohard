package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
)

// TestUncheckedQuiet: a unit that depends on an opaque unit reports
// only that it was not checked: a name it reads from the opaque unit
// (which GoHard cannot see into) is no error of its own. Its syntax
// errors still are.
func TestUncheckedQuiet(t *testing.T) {
	files := map[string]string{
		"p.vhd": `package p is
  type pt is protected
    procedure set(v : integer);
  private
    variable x : integer;
  end protected;
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
		"p.vhd:4:3: unsupported/construct",
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
