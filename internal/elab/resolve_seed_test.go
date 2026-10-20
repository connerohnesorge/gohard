package elab

import (
	"fmt"
	"strings"
	"testing"
)

// TestResolvedSeedIndependent: a resolved std_logic signal with several
// drivers settles to resolved(all driver values) at every seed, whether
// the drivers are processes, concurrent assignments or a for-generate.
// The in-delta order a seed picks must never change the outcome.
func TestResolvedSeedIndependent(t *testing.T) {
	const lits = "01ZXLHWU-"
	for c := 0; c < 24; c++ {
		n := 2 + c%3
		var vs []string
		for i := 0; i < n; i++ {
			vs = append(vs, fmt.Sprintf("'%c'", lits[(c*7+i*5)%9]))
		}
		var body strings.Builder
		for i, v := range vs {
			switch c % 3 {
			case 0:
				fmt.Fprintf(&body,
					"  p%d : process begin s <= %s; wait; end process;\n",
					i, v)
			default:
				fmt.Fprintf(&body, "  s <= %s;\n", v)
			}
		}
		src := `library ieee;
use ieee.std_logic_1164.all;
entity tb is end entity;
architecture a of tb is
  signal s : std_logic;
begin
` + body.String() + `  chk : process begin
    wait for 1 ns;
    assert s = resolved(std_ulogic_vector'(` + strings.Join(vs, ", ") +
			`)) report "bad " & std_logic'image(s) severity error;
    wait;
  end process;
end architecture;
`
		for seed := uint64(0); seed < 8; seed++ {
			r := simulate(t, src, "tb", runOpts{seed: seed})
			if len(r.diags) != 0 {
				t.Fatalf("case %d %v seed %d: %v", c, vs, seed,
					r.diags[0].Message)
			}
		}
	}
	gen := `library ieee;
use ieee.std_logic_1164.all;
entity tb is end entity;
architecture a of tb is
  signal s : std_logic;
begin
  g : for i in 0 to 3 generate
    p : process begin
      if i = 2 then s <= '1'; else s <= 'Z'; end if;
      wait;
    end process;
  end generate;
  chk : process begin
    wait for 1 ns;
    assert s = '1' report "bad " & std_logic'image(s) severity error;
    wait;
  end process;
end architecture;
`
	for seed := uint64(0); seed < 8; seed++ {
		if r := simulate(t, gen, "tb", runOpts{seed: seed}); len(r.diags) != 0 {
			t.Fatalf("generate seed %d: %v", seed, r.diags[0].Message)
		}
	}
}
