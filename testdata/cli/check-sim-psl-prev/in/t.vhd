library ieee; use ieee.std_logic_1164.all;
entity t is end;
architecture rtl of t is
  signal clk, a, b : std_logic := '0';
  default clock is rising_edge(clk);
begin
  clk <= not clk after 5 ns;
  a <= '1' after 20 ns;
  b <= '1' after 30 ns;
  x: assert always (prev(a) = '1' -> b = '1');
  stopper: process begin wait for 100 ns; std.env.finish; end process;
end;
