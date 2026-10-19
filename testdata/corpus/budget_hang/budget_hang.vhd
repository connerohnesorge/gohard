-- budget_hang: design under test for the budget_hang corpus design (#8).
-- A free-running 8-bit counter with a synchronous reset.
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity budget_hang is
  port (
    clk   : in  std_logic;
    rst   : in  std_logic;
    count : out unsigned(7 downto 0)
  );
end entity budget_hang;

architecture rtl of budget_hang is
  signal q : unsigned(7 downto 0) := (others => '0');
begin
  count <= q;

  tick : process (clk)
  begin
    if rising_edge(clk) then
      if rst = '1' then
        q <= (others => '0');
      else
        q <= q + 1;
      end if;
    end if;
  end process tick;
end architecture rtl;
