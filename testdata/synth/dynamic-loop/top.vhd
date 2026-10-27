library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity top is
  port (
    x : in  unsigned(7 downto 0);
    n : out natural range 0 to 8
  );
end entity;

architecture rtl of top is
begin
  -- Count trailing zeros: the trip count depends on the input value.
  ctz : process (x)
    variable i : natural range 0 to 8;
  begin
    i := 0;
    while i < 8 and x(i) = '0' loop
      i := i + 1;
    end loop;
    n <= i;
  end process;
end architecture;
