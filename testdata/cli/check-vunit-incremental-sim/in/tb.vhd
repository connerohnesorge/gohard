library ieee;
use ieee.std_logic_1164.all;
entity tb is end entity;
architecture sim of tb is
  signal clk, a, b : std_logic := '0';
  component dut_c is
    port (clk : in std_logic; a, b : in std_logic);
  end component;
  for u1 : dut_c use entity work.dut(rtl);
begin
  u1 : dut_c port map (clk, a, b);
  clk <= not clk after 5 ns when now < 60 ns;
  process begin
    wait for 22 ns; a <= '1';
    wait for 10 ns; a <= '0'; b <= '1';
    wait for 10 ns; b <= '0';
    wait;
  end process;
end architecture;
