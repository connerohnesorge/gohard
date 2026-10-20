library ieee;
use ieee.std_logic_1164.all;
package defs is
  type pair_t is record
    first  : integer;
    second : std_logic;
  end record;
  constant ZERO : integer := 0;
  function inc(x : integer) return integer;
  function inc(x : std_logic) return std_logic;
end package;
