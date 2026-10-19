-- psl_simple: a cycle counter drives a and b on the falling edge, and
-- three clocked directives sample them on the rising edge.
--   k (rising edge at 5 + 10*k ns): a = '1' at k = 2, 5; b = '1' at 3, 5
--   assert always not (a and b)  fails at k = 5 (55 ns)
--   cover {a; b}                  hits  at k = 3 (35 ns)
--   cover {b; b}                  never hits (b is never high twice in a
--                                 row)
library ieee;
use ieee.std_logic_1164.all;

entity psl_simple is
  port (
    clk : in  std_logic;
    a   : out std_logic;
    b   : out std_logic
  );
end entity psl_simple;

architecture rtl of psl_simple is
  signal n  : natural := 0;
  signal ai : std_logic := '0';
  signal bi : std_logic := '0';

  default clock is rising_edge(clk);
begin
  count : process (clk)
  begin
    if falling_edge(clk) then
      n <= n + 1;
    end if;
  end process count;

  ai <= '1' when n = 2 or n = 5 else '0';
  bi <= '1' when n = 3 or n = 5 else '0';
  a  <= ai;
  b  <= bi;

  never_both : assert always not (ai = '1' and bi = '1')
    report "a and b high in the same cycle";

  a_then_b : cover {ai = '1'; bi = '1'}
    report "a then b";

  b_twice : cover {bi = '1'; bi = '1'}
    report "b twice in a row";
end architecture rtl;
