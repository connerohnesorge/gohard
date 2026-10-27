library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk : in  std_ulogic;
    d   : in  std_ulogic;
    q   : out std_ulogic
  );
end entity;

architecture rtl of top is
begin
  latch : block (clk = '1' and clk'event)
  begin
    q <= guarded d;
  end block;
end architecture;
