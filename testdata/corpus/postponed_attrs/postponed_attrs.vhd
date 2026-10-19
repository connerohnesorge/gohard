-- postponed_attrs: postponed processes and signal attributes.
--
-- `watch` and `late_watch` are both sensitive to d. When d glitches
-- through several delta cycles within one time step, `watch` runs once
-- per delta event, while the postponed process `late_watch` runs once,
-- in the last cycle of the time step, and sees only the settled value.
-- A postponed process may not cause a delta cycle, so it publishes its
-- results with a 1 ns delay.
library ieee;
use ieee.std_logic_1164.all;

entity postponed_attrs is
  port (
    d      : in  std_logic;
    clk    : in  std_logic;
    q_dly  : out std_logic;
    edges  : out natural;
    n_runs : out natural;
    p_runs : out natural;
    p_seen : out std_logic
  );
end entity postponed_attrs;

architecture rtl of postponed_attrs is
begin
  -- 'delayed with an explicit delay.
  q_dly <= d'delayed(5 ns);

  -- 'event and 'last_value: only a 0 -> 1 change counts as an edge.
  edge_count : process (clk) is
    variable n : natural := 0;
  begin
    if clk'event and clk = '1' and clk'last_value = '0' then
      n := n + 1;
    end if;
    edges <= n;
  end process edge_count;

  watch : process (d) is
    variable n : natural := 0;
  begin
    n      := n + 1;
    n_runs <= n;
  end process watch;

  late_watch : postponed process (d) is
    variable n : natural := 0;
  begin
    n      := n + 1;
    p_runs <= n after 1 ns;
    p_seen <= d after 1 ns;
  end process late_watch;
end architecture rtl;
