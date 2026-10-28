-- counter: its output's type is visible through its own use clause
-- only (#57).
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
use work.types.all;

entity counter is
  port (
    clk   : in std_logic;
    count : out word_t
  );
end entity counter;

architecture rtl of counter is
  signal q : unsigned(7 downto 0) := (others => '0');
begin
  count <= std_logic_vector(q);

  tick : process (clk) is
  begin
    if rising_edge(clk) then
      q <= q + 1;
    end if;
  end process tick;
end architecture rtl;
