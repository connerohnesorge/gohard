library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity top is
  port (
    x   : in  unsigned(7 downto 0);
    res : out unsigned(7 downto 0)
  );
end entity top;

architecture rtl of top is
  -- Sum of the two operands.
  signal added : unsigned(7 downto 0);
begin
  res <= added + x;
end architecture rtl;
