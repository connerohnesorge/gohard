library ieee;
use ieee.std_logic_1164.all;

library unisim;
use unisim.vcomponents.all;

entity top is
  port (
    clk_in : in  std_logic;
    clk    : out std_logic
  );
end entity top;

architecture rtl of top is
begin
  u_bufg : BUFG
    port map (O => clk, I => clk_in);
end architecture rtl;
