-- top: a component instantiation of counter in a unit that does not
-- use work.types (#57).
library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk   : in std_logic;
    count : out std_logic_vector(7 downto 0)
  );
end entity top;

use work.types.all;

architecture rtl of top is
  component counter is
    port (
      clk   : in std_logic;
      count : out word_t
    );
  end component counter;
begin
  u0 : counter
    port map (
      clk   => clk,
      count => count
    );
end architecture rtl;
