library ieee;
use ieee.std_logic_1164.all;

entity clean is
  generic (FAST : boolean := true);
  port (clk, a, b, en : in std_logic; sel : in std_logic_vector(1 downto 0);
        x, y, z, w, q : out std_logic);
end entity;

architecture rtl of clean is
  procedure pick (signal i : in std_logic; signal o : out std_logic) is
  begin
    o <= i;
  end procedure;
begin
  defaults : process (a, en)
  begin
    x <= '0';
    if en = '1' then
      x <= a;
    end if;
  end process;

  complete : process (sel, a, b)
  begin
    case sel is
      when "00"   => y <= a;
      when "01"   => y <= b;
      when others => y <= '0';
    end case;
  end process;

  on_generic : process (a, b, en)
  begin
    if FAST then
      z <= a;
    end if;
    if en = '1' then
      pick(b, w);
    else
      w <= a;
    end if;
  end process;

  clocked : process (clk)
  begin
    if rising_edge(clk) then
      if en = '1' then
        q <= a;
      end if;
    end if;
  end process;
end architecture;
