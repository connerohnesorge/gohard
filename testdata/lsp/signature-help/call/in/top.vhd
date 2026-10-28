-- The design of the inlay hint and signature help fixtures (#56).
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity adder is
  generic (WIDTH : natural := 8; DEPTH : natural := 2 ** 4);
  port (
    a, b : in  unsigned(WIDTH - 1 downto 0);
    sum  : out unsigned(WIDTH - 1 downto 0)
  );
end entity adder;

architecture rtl of adder is
begin
  sum <= a + b;
end architecture rtl;

library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity top is
end entity top;

architecture sim of top is
  constant BYTES     : natural := 4;
  constant BITS      : natural := BYTES * 8;
  constant MASK      : std_logic_vector(7 downto 0) := x"0F" or x"30";
  -- Limits value to at most limit.
  function clip(value : integer; limit : integer) return integer is
  begin
    if value > limit then
      return limit;
    end if;
    return value;
  end function clip;
  constant TOP_VALUE : integer := clip(300, 255);
  signal x, y, s     : unsigned(7 downto 0);
  constant limit     : integer := 3;
  alias low_nibble is x(3 downto 0);
  alias carry : std_logic is s(7);
begin
  u_add : entity work.adder
    generic map (8)
    port map (x, y, sum => s);

  check : process
    variable acc : integer := 0;
  begin
    for i in 0 to BYTES - 1 loop
      acc := clip(acc + i, limit);
    end loop;
    report integer'image(to_integer(x));
    wait;
  end process check;
end architecture sim;
