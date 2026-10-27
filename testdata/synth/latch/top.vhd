library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    en : in  std_ulogic;
    d  : in  std_ulogic;
    q  : out std_ulogic
  );
end entity;

architecture rtl of top is
begin
  -- q keeps its value when en = '0': level-sensitive storage.
  comb : process (en, d)
  begin
    if en = '1' then
      q <= d;
    end if;
  end process;
end architecture;
