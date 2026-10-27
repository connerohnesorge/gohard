library ieee;
use ieee.std_logic_1164.all;

entity clean is
  generic (INVERT : boolean := false);
  port (clk, a, b : in std_logic; v : in std_logic_vector(3 downto 0);
        x, y, z, q : out std_logic);
end entity;

architecture rtl of clean is
  alias b_a is b;
begin
  listed : process (a, b_a, v)
  begin
    if INVERT then
      x <= not (a and b);
    else
      x <= a and b and v(v'left);
    end if;
  end process;

  all_of_them : process (all)
  begin
    y <= a or b;
  end process;

  clocked : process (clk)
  begin
    if rising_edge(clk) then
      q <= a xor b;
    end if;
  end process;

  z <= v(v'right);
end architecture;
