-- vhdl93_only_count: counts the rising edges of tick in a shared
-- variable of type natural (#8, #63). VHDL-1993 allows a shared
-- variable of any type; from VHDL-2002 on it must be of a protected
-- type (LRM 6.4.2.4), so analysis under --std=02, 08 or 19 fails on
-- its declaration.
library ieee;
use ieee.std_logic_1164.all;

entity vhdl93_only_count is
  port (
    tick  : in  std_logic;
    count : out natural
  );
end vhdl93_only_count;

architecture rtl of vhdl93_only_count is
  shared variable hits : natural := 0;
begin
  -- up writes hits only on a rising edge and show reads it only on a
  -- falling one, so the two processes never touch it in the same
  -- simulation cycle and the count does not depend on their order.
  up : process (tick)
  begin
    if rising_edge(tick) then
      hits := hits + 1;
    end if;
  end process up;

  show : process (tick)
  begin
    if falling_edge(tick) then
      count <= hits;
    end if;
  end process show;
end rtl;
