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
  signal s : std_logic register;
  disconnect s : std_logic after 0 ns;
begin
  drv : block (en = '1')
  begin
    s <= guarded d;
  end block;

  q <= s;
end architecture;
