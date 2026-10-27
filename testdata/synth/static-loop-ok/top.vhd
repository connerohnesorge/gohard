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
  -- Same function with a constant trip count and an exit.
  ctz : process (x)
    variable c : natural range 0 to 8;
  begin
    c := 8;
    for i in 0 to 7 loop
      if x(i) = '1' then
        c := i;
        exit;
      end if;
    end loop;
    n <= c;
  end process;
end architecture;
