-- top: a component instantiation of counter without its component
-- declaration (#57).
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity top is
  port (
    clk   : in std_logic;
    rst   : in std_logic;
    count : out unsigned(7 downto 0)
  );
end entity top;

architecture rtl of top is
  signal enable : std_logic; -- drives en
begin
  enable <= '1';

  u0 : counter
    port map (
      clk   => clk,
      rst   => rst,
      en    => enable,
      count => count
    );
end architecture rtl;
