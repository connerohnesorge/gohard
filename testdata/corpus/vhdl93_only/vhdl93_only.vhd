-- vhdl93_only: design valid only under VHDL-1993 (#8).
-- It uses identifiers that later standards reserve:
--   protected                 reserved since VHDL-2002
--   context, force, release   reserved since VHDL-2008
-- so analysis under --std=02, 08 or 19 must fail on the first of them,
-- while VHDL-1993 accepts the whole file.
library ieee;
use ieee.std_logic_1164.all;

entity vhdl93_only is
  port (
    force   : in  std_logic;
    release : in  std_logic;
    context : out std_logic
  );
end entity vhdl93_only;

architecture rtl of vhdl93_only is
  signal protected : std_logic := '0';
begin
  -- A set/reset latch: force sets, release clears, release wins.
  hold : process (force, release)
  begin
    if release = '1' then
      protected <= '0';
    elsif force = '1' then
      protected <= '1';
    end if;
  end process hold;

  context <= protected;
end architecture rtl;
