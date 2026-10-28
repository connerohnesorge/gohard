-- psl_vunit: the design under a PSL verification unit (#66). A cycle
-- counter drives req and ack on the falling edge, as in psl_halt; the
-- directives that check them are not here but in the vunit
-- psl_vunit_checks (psl_vunit_props.vhd), bound to psl_vunit(rtl).
--   k (rising edge at 5 + 10*k ns): req = '1' at k = 2, 5; ack = '1' at
--   k = 3, 7
library ieee;
use ieee.std_logic_1164.all;

entity psl_vunit is
  port (
    clk : in  std_logic;
    req : out std_logic;
    ack : out std_logic
  );
end entity psl_vunit;

architecture rtl of psl_vunit is
  signal n    : natural := 0;
  signal reqi : std_logic := '0';
  signal acki : std_logic := '0';
begin
  count : process (clk)
  begin
    if falling_edge(clk) then
      n <= n + 1;
    end if;
  end process count;

  reqi <= '1' when n = 2 or n = 5 else '0';
  acki <= '1' when n = 3 or n = 7 else '0';
  req  <= reqi;
  ack  <= acki;
end architecture rtl;
