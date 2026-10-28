-- top: the name of the entity to instantiate, typed without its `;`
-- where the instance goes, before another statement (#57).
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity top is
  port (
    clk   : in std_logic;
    rst   : in std_logic;
    count : out unsigned(7 downto 0);
    led   : out std_logic
  );
end entity top;

architecture rtl of top is
begin
  counter
  led <= rst;
end architecture rtl;
