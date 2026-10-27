library ieee;
use ieee.std_logic_1164.all;

package defs is
  type state_t is (IDLE, BUSY);
  type pair_t is record
    first  : integer;
    second : std_logic;
  end record;
  constant WIDTH : natural := 8;
  function inc(x : integer) return integer;
  procedure clear(signal s : out std_logic);
end package;

package body defs is
  function inc(x : integer) return integer is
  begin
    return x + 1;
  end function;

  procedure clear(signal s : out std_logic) is
  begin
    s <= '0';
  end procedure;
end package body;
