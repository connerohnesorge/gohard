library ieee;
use ieee.std_logic_1164.all;
use work.defs.all;

entity top is
end entity;

architecture rtl of top is
  signal p : pair_t;
  signal n : integer;
begin
  -- Two comment lines
  -- fold together.
  process
  begin
    n <= p.first;
    n <= inc(true);
    p.second <= std_logic'('1');
    n <= n + ZERO;
    wait;
  end process;
end architecture;
