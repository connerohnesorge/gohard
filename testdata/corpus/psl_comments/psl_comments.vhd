-- psl_comments: psl_simple's counter, with every PSL item written as a
-- `-- psl` comment (psl.comments, #109). The default clock is a comment
-- in the statement part, and it clocks the directives after it.
--   k (rising edge at 5 + 10*k ns): a = '1' at k = 2, 5; b = '1' at 3, 6
--   assert always (a -> next b)  holds
--   cover {a; b}                 hits at k = 3 (35 ns) and 6 (65 ns)
-- Each directive is on one line: the layout variant puts a blank line
-- after every line, and GoHard ends a `-- psl` directive at a line that
-- does not start with `--` (GHDL and NVC read on to the `;`).
library ieee;
use ieee.std_logic_1164.all;

entity psl_comments is
  port (
    clk : in  std_logic;
    a   : out std_logic;
    b   : out std_logic
  );
end entity psl_comments;

architecture rtl of psl_comments is
  signal n  : natural := 0;
  signal ai : std_logic := '0';
  signal bi : std_logic := '0';
begin
  count : process (clk)
  begin
    if falling_edge(clk) then
      n <= n + 1;
    end if;
  end process count;

  ai <= '1' when n = 2 or n = 5 else '0';
  bi <= '1' when n = 3 or n = 6 else '0';
  a  <= ai;
  b  <= bi;

  -- psl default clock is rising_edge(clk);

  -- psl b_follows_a : assert always (ai = '1' -> next bi = '1');

  -- psl a_then_b : cover {ai = '1'; bi = '1'} report "a then b";
end architecture rtl;
