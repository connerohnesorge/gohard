library ieee;
use ieee.std_logic_1164.all;
use work.defs.all;

-- The token types and modifiers of #55.
entity top is
  generic (N : positive := 2);
  port (
    clk : in  std_logic;
    q   : out std_logic_vector(WIDTH - 1 downto 0)
  );
end entity;

architecture rtl of top is
  signal st   : state_t := IDLE;
  signal p    : pair_t;
  signal bits : bit_vector(3 downto 0) := x"A";
  alias first_bit : bit is bits(0);
begin
  process (clk)
    variable count : integer := 0;
  begin
    if rising_edge(clk) then
      count := inc(count);
      for i in 1 to N loop
        p.first <= i;
      end loop;
      clear(p.second);
      st <= BUSY after 10 ns;
      report "count " & integer'image(count) severity note;
    end if;
  end process;
  q <= (others => first_bit ?= '1');
end architecture;
