library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity tb_top is
end entity tb_top;

architecture sim of tb_top is
  signal clk   : std_logic := '0';
  signal count : unsigned(3 downto 0);
begin
  clk <= not clk after 5 ns;

  dut : entity work.top
    port map (clk => clk, count => count);

  check : process
  begin
    wait for 100 ns;
    assert count /= 0 report "the counter did not count" severity error;
    wait;
  end process check;
end architecture sim;
