package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
)

// TestTypeAttributePrefix: 'IMAGE, 'VALUE, 'POS and the other function
// attributes of LRM 16.2.2 need a type or subtype prefix; a function,
// a signal or an entity is one error at the prefix. VHDL-2019 allows
// object'IMAGE without an argument.
func TestTypeAttributePrefix(t *testing.T) {
	src := `entity top2 is end;
architecture a of top2 is
  function h return integer is begin return 1; end;
  signal s : bit;
  type e_t is (x, y);
begin
  process
    variable v : integer;
  begin
    report h'image(1);
    report s'image(1);
    report top2'image(1);
    report integer'image(1) & e_t'image(x) & bit'image(s);
    v := e_t'pos(y) + integer'value("1") + s'subtype'pos('1');
    wait;
  end process;
end;
`
	want := []string{
		"b.vhd:10:12: sema/error: the prefix of 'image must be a type, " +
			"not h",
		"b.vhd:11:12: sema/error: the prefix of 'image must be a type, " +
			"not s",
		"b.vhd:12:12: sema/error: the prefix of 'image must be a type, " +
			"not top2",
	}
	for _, std := range []string{"2008", "2019"} {
		r := analyze(t, map[string]string{"b.vhd": src},
			func(c *config.Config) { c.Standard = std })
		got := strings.TrimSpace(diagText(r))
		if got != strings.Join(want, "\n") {
			t.Errorf("%s: got\n%s", std, got)
		}
	}
	obj := strings.Replace(src, "report s'image(1);", "report s'image;", 1)
	r := analyze(t, map[string]string{"b.vhd": obj},
		func(c *config.Config) { c.Standard = "2019" })
	if got := diagText(r); strings.Contains(got, ":11:") {
		t.Errorf("2019 s'image: %s", got)
	}
}
