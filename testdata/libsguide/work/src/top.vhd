library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity top is
  port (
    clk  : in  std_logic;
    leds : out unsigned(7 downto 0)
  );
end entity top;

architecture rtl of top is
begin
  u_counter : entity work.counter
    port map (clk => clk, count => leds);
end architecture rtl;
