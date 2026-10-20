library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity counter is
  port (
    clk   : in  std_logic;
    count : out unsigned(7 downto 0)
  );
end entity counter;

architecture rtl of counter is
  signal n : unsigned(7 downto 0) := (others => '0');
begin
  n     <= n + 1 when rising_edge(clk);
  count <= n;
end architecture rtl;
