-- psl_named_formals: the formals variant of psl_named (#66). The
-- property ack_follows becomes follows (boolean r, a), instantiated as
-- follows(reqi = '1', acki = '1'); the outcome is the same.
-- psl_named: named PSL property and sequence instances and the
-- next_event family in simulation (#66). A cycle counter drives req and
-- ack on the falling edge, as in psl_halt; the directives sample them
-- on the rising edge.
--   k (rising edge at 5 + 10*k ns, n = k there): req = '1' at k = 2, 5;
--   ack = '1' at k = 3, 7
--   cover {handshake(req, ack)}       hits at k = 3 (35 ns)
--   assert follows(req, ack)          holds at k = 2, fails at k = 6
--                                     (65 ns)
--   next_event(ack)(n < 7) after req  holds for k = 2 (ack at 3),
--                                     fails for k = 5 (ack at 7, 75 ns)
--   next_event_a!(clk)[2 to 3](n /= 4) after req fails for k = 2 at
--                                     k = 4 (45 ns)
--   next_event_e(ack)[1 to 2](n = 7) after req holds (ack at k = 7)
-- The declarations are in the statement part, where GHDL 6.0.0 accepts
-- them (it rejects them in the declarative part, where GoHard also
-- accepts them, LRM 3.3.2).
library ieee;
use ieee.std_logic_1164.all;

entity psl_named is
  port (
    clk : in  std_logic;
    req : out std_logic;
    ack : out std_logic
  );
end entity psl_named;

architecture rtl of psl_named is
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

  sequence handshake (boolean r, a) is {r; a};

  property follows (boolean r, a) is always (r -> next a);

  req_ack : cover {handshake(reqi = '1', acki = '1')}
    report "req then ack";

  ack_next : assert follows(reqi = '1', acki = '1')
    report "ack did not follow req";

  ack_event : assert always (reqi = '1' ->
      next_event(acki = '1')(n < 7))
    report "ack came at 7";

  ack_all : assert always (reqi = '1' ->
      next_event_a!(clk = '1')[2 to 3](n /= 4))
    report "n reached 4 after a req";

  ack_some : assert always (reqi = '1' ->
      next_event_e(acki = '1')[1 to 2](n = 7))
    report "no ack at n = 7";
end architecture rtl;
