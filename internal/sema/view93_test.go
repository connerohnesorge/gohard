package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
)

// TestView93TypeModel pins the recorded decision for the 1993 and 2002
// views of the bundle (internal/stdlib/README.md, "The 2008 type model
// before 2008"): only the 1076-2019 text ships (#33), so every view
// analyzes it and hides names, and the types keep their 2008 model.
// STD_LOGIC_VECTOR is a subtype of STD_ULOGIC_VECTOR in every view,
// so under 1993 and 2002 GoHard
//
//   - accepts mixing the two without a conversion, which GHDL --std=93
//     and NVC --std=1993 reject (a missed error, never a false one);
//   - reports two overloads that differ only between the two types as
//     homographs, which 1993 tools accept as distinct (a false error:
//     GHDL and NVC agree it is valid VHDL-93).
//
// A per-standard type model would need a second, 1993 text of
// std_logic_1164 (its separate STD_LOGIC_VECTOR overloads), which
// cannot ship under #13 entry 7. Changing either outcome changes the
// decision: update the README with it.
func TestView93TypeModel(t *testing.T) {
	mix := `library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
entity e is end entity;
architecture a of e is
  signal s : std_logic_vector(3 downto 0);
  signal u : std_ulogic_vector(3 downto 0);
  signal n : unsigned(3 downto 0);
begin
  s <= u;
  n <= unsigned(s) + 1;
end architecture;
`
	pair := `library ieee;
use ieee.std_logic_1164.all;
package p is
  function f(x : std_logic_vector) return integer;
  function f(x : std_ulogic_vector) return integer;
end package;
`
	for _, std := range []string{"1993", "2002", "2008", "2019"} {
		cfg := func(c *config.Config) { c.Standard = std }
		r := analyze(t, map[string]string{"mix.vhd": mix}, cfg)
		if got := diagText(r); got != "" {
			t.Errorf("%s: mixing the vector types: want accepted, got\n%s",
				std, got)
		}
		r = analyze(t, map[string]string{"pair.vhd": pair}, cfg)
		if got := diagText(r); !strings.Contains(got,
			"pair.vhd:5:12: sema/error: f is already declared") {
			t.Errorf("%s: overloads on both vector types: want the "+
				"homograph error, got\n%s", std, got)
		}
	}
}
