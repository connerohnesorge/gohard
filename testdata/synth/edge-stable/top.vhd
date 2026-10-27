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
  -- A clock-edge spelling built from 'stable.
  reg : process (clk)
  begin
    if not clk'stable and clk = '1' then
      q <= d;
    end if;
  end process;
end architecture;
