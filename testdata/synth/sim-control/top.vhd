library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk  : in std_ulogic;
    done : in std_ulogic
  );
end entity;

architecture rtl of top is
begin
  stopper : process (clk)
  begin
    if rising_edge(clk) then
      if done = '1' then
        std.env.finish;
      end if;
    end if;
  end process;
end architecture;
