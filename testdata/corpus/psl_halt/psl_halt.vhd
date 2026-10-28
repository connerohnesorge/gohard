-- psl_halt: a PSL assert halts the run at the default
-- simulation.stop_level, error (#35, #63). A cycle counter drives req
-- and ack on the falling edge; two clocked directives sample them on
-- the rising edge.
--   k (rising edge at 5 + 10*k ns): req = '1' at k = 2, 5; ack = '1' at
--   k = 3, 7
--   assert always (req -> next ack)  holds at k = 2, fails at k = 6
--                                    (65 ns): the run halts there
--   cover {req; ack}                 hits at k = 3 (35 ns)
library ieee;
use ieee.std_logic_1164.all;

entity psl_halt is
  port (
    clk : in  std_logic;
    req : out std_logic;
    ack : out std_logic
  );
end entity psl_halt;

architecture rtl of psl_halt is
  signal n    : natural := 0;
  signal reqi : std_logic := '0';
  signal acki : std_logic := '0';

  default clock is rising_edge(clk);
begin
  count : process (clk)
  begin
    if falling_edge(clk) then
      n <= n + 1;
    end if;
  end process count;

  reqi <= '1' when n = 2 or n = 5 else '0';
  acki <= '1' when n = 3 or n = 7 else '0';
  req  <= reqi;
  ack  <= acki;

  ack_next : assert always (reqi = '1' -> next acki = '1')
    report "ack did not follow req";

  req_ack : cover {reqi = '1'; acki = '1'}
    report "req then ack";
end architecture rtl;
