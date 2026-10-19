library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
use work.defs.all;

entity top is
  port (clk : in std_logic; q : out unsigned(WIDTH - 1 downto 0));
end entity top;

architecture rtl of top is
  signal count : unsigned(WIDTH - 1 downto 0);
begin
  q <= count;
  process (clk) is
  begin
    if rising_edge(clk) then
      count <= count + step;
      count <= resize(count, true);
    end if;
  end process;
end architecture rtl;
