library ieee;
use ieee.std_logic_1164.all;

entity sens is
  port (clk, rst, a, b, sel : in std_logic; y, z, q : out std_logic);
end entity;

architecture rtl of sens is
  signal v : std_logic_vector(3 downto 0);
begin
  comb : process (a, sel)
  begin
    if sel = '1' then
      y <= a and b;
    else
      y <= v(0);
    end if;
  end process;

  ok : process (a, b, v)
  begin
    z <= a or b or v(v'left);
  end process;

  reg : process (clk, rst)
  begin
    if rst = '1' then
      q <= '0';
    elsif rising_edge(clk) then
      q <= a;
    end if;
  end process;

  v <= a & b & a & b;
end architecture;
