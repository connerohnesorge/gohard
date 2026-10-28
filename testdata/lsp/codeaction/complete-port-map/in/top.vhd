-- top: an instance of counter whose port map names only the clock
-- (#57).
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
begin
  u0 : entity work.counter
    port map (
      clk => clk -- the only clock
    );
end architecture rtl;
