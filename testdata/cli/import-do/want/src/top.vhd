library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

library mylib;
use mylib.pkg.all;

entity top is
  port (
    clk   : in  std_logic;
    count : out unsigned(WIDTH - 1 downto 0)
  );
end entity top;

architecture rtl of top is
  signal n : unsigned(WIDTH - 1 downto 0) := (others => '0');
begin
  process (clk)
  begin
    if rising_edge(clk) then
      n <= n + 1;
    end if;
  end process;
  count <= n;
end architecture rtl;
