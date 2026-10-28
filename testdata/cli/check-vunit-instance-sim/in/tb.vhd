library ieee;
use ieee.std_logic_1164.all;
entity tb is end entity;
architecture sim of tb is
  signal clk, a, b : std_logic := '0';
begin
  u : entity work.dut(rtl) port map (clk, a, b);
  clk <= not clk after 5 ns when now < 100 ns;
  process begin
    wait for 22 ns; a <= '1';
    wait for 10 ns; a <= '0'; b <= '1';
    wait for 10 ns; b <= '0';
    wait for 20 ns; a <= '1';
    wait for 10 ns; a <= '0';
    wait;
  end process;
end architecture;
