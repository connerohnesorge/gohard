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
  -- Two while loops whose conditions depend only on constants and on
  -- variables the loop sets from constants: GHDL unrolls both. One is
  -- a clog2 helper for a constant, one runs in the process.
  function clog2 (v : positive) return natural is
    variable p : natural := 0;
  begin
    while 2 ** p < v loop
      p := p + 1;
    end loop;
    return p;
  end function;

  constant W : natural := clog2(200);
begin
  ones : process (x)
    variable i : natural range 0 to 8;
    variable c : natural range 0 to 8;
  begin
    i := 0;
    c := 0;
    while i < W loop
      if x(i) = '1' then
        c := c + 1;
      end if;
      i := i + 1;
    end loop;
    n <= c;
  end process;
end architecture;
