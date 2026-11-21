library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity alu is
end entity;

architecture rtl of alu is
  function pick(sel : std_logic; a, b : unsigned) return unsigned is
  begin
    return a;
  end function;
  signal res : unsigned(7 downto 0);
  signal op : std_logic;
begin
  res <= pick(op, res, res);
end architecture;
