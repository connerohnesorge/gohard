package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
)

// TestLRMContexts: the IEEE 1076-2008 context declarations
// IEEE_STD_CONTEXT and IEEE_BIT_CONTEXT (16.1) are bundled units of
// ieee from VHDL-2008 on, make exactly the packages they name visible,
// and are absent before 2008.
func TestLRMContexts(t *testing.T) {
	std := func(s string) func(*config.Config) {
		return func(c *config.Config) { c.Standard = s }
	}
	for _, s := range []string{"2008", "2019"} {
		noErrors(t, analyze(t, map[string]string{"t.vhd": `library ieee;
context ieee.ieee_std_context;
entity t is end;
architecture a of t is
  signal l : std_logic_vector(3 downto 0);
  signal u : unsigned(3 downto 0) := to_unsigned(5, 4);
begin
  l <= std_logic_vector(u);
end;
`, "b.vhd": `library ieee;
context ieee.IEEE_BIT_CONTEXT;
entity b is end;
architecture a of b is
  signal u : unsigned(3 downto 0) := to_unsigned(5, 4);
  signal v : bit_vector(3 downto 0) := bit_vector(u);
begin
end;
`}, std(s)))
	}
	// The bit context does not make std_logic_1164 visible.
	r := analyze(t, map[string]string{"t.vhd": `library ieee;
context ieee.ieee_bit_context;
entity t is end;
architecture a of t is
  signal l : std_logic;
begin
end;
`}, std("2008"))
	if got := diagText(r); !strings.Contains(got,
		"std_logic is not declared") {
		t.Errorf("bit context: want std_logic undeclared, got\n%s", got)
	}
	// Before 2008 the units do not exist.
	r = analyze(t, map[string]string{"t.vhd": `library ieee;
use ieee.ieee_std_context.all;
entity t is end;
`}, std("2002"))
	if got := diagText(r); !strings.Contains(got,
		"library ieee has no unit ieee_std_context") {
		t.Errorf("2002: want missing unit, got\n%s", got)
	}
	if ds := Bundle().Diagnostics; len(ds) != 0 {
		t.Errorf("bundle diagnostics: %v", ds)
	}
}
