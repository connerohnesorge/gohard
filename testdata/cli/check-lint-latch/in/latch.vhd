library ieee;
use ieee.std_logic_1164.all;

entity lat is
  port (a, b, en : in std_logic; sel : in std_logic_vector(1 downto 0);
        y, z, w, x, v : out std_logic);
end entity;

architecture rtl of lat is
begin
  -- y: latch (no else); z: no latch (default first).
  p1 : process (all)
  begin
    z <= '0';
    if en = '1' then
      y <= a;
      z <= b;
    end if;
  end process;

  -- w: latch (one case alternative misses it); x: assigned in all.
  p2 : process (sel, a, b)
  begin
    case sel is
      when "00"   => w <= a; x <= a;
      when "01"   => w <= b; x <= b;
      when others => x <= '0';
    end case;
  end process;

  -- v: no latch (if/elsif/else all assign).
  p3 : process (en, a, b)
  begin
    if en = '1' then
      v <= a;
    elsif a = '1' then
      v <= b;
    else
      v <= '0';
    end if;
  end process;
end architecture;
