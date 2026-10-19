-- postponed_attrs_tb: self-checking testbench for postponed_attrs.
library ieee;
use ieee.std_logic_1164.all;

entity postponed_attrs_tb is
end entity postponed_attrs_tb;

architecture tb of postponed_attrs_tb is
  signal s      : std_logic := '0';
  signal r      : std_logic := '0';
  signal never  : std_logic := 'L';
  signal g      : std_logic := '1';
  signal g_lows : natural   := 0;
  signal n_tx   : natural   := 0;
  signal d      : std_logic := '0';
  signal clk    : std_logic := '0';
  signal q_dly  : std_logic;
  signal edges  : natural;
  signal n_runs : natural;
  signal p_runs : natural;
  signal p_seen : std_logic;
  signal q_ev75 : natural   := 0;
begin
  dut : entity work.postponed_attrs
    port map (
      d      => d,
      clk    => clk,
      q_dly  => q_dly,
      edges  => edges,
      n_runs => n_runs,
      p_runs => p_runs,
      p_seen => p_seen
    );

  -- s: event at 10 ns, transaction without event at 20 ns, event at
  -- 30 ns.
  s   <= transport '1' after 10 ns, '1' after 20 ns, '0' after 30 ns;
  clk <= transport '1' after 40 ns, '0' after 45 ns, 'X' after 50 ns,
         '1' after 55 ns, '0' after 60 ns, '1' after 65 ns;

  -- 'transaction toggles on every transaction of s, events or not.
  count_tx : process (s'transaction) is
  begin
    if now > 0 ns then
      n_tx <= n_tx + 1;
    end if;
  end process count_tx;

  -- An ordinary process sees every delta value of g.
  g_watch : process (g) is
  begin
    if g = '0' then
      g_lows <= g_lows + 1;
    end if;
  end process g_watch;

  -- A postponed concurrent assertion sees only the settled value of g,
  -- so the delta glitch to '0' at 20 ns never triggers it.
  postponed assert g = '1' report "postponed assert saw a glitch"
    severity error;

  attrs : process is
  begin
    -- A signal without events: 'last_value is its value and
    -- 'last_event is TIME'HIGH.
    assert never'last_value = 'L' report "never'last_value"
      severity error;
    assert never'last_event = time'high report "never'last_event"
      severity error;

    -- 'driving and 'driving_value: the driver changes only when the
    -- transaction matures, one delta later.
    r <= '1';
    assert r'driving report "r'driving" severity error;
    assert r'driving_value = '0' report "r'driving_value before"
      severity error;
    wait for 0 ns;
    assert r'driving_value = '1' report "r'driving_value after"
      severity error;

    -- 10 ns: the event on s, seen in the delta where s changes.
    wait on s;
    assert now = 10 ns report "s event time" severity error;
    assert s'event and s'active report "s'event at 10 ns"
      severity error;
    assert s'last_value = '0' report "s'last_value at 10 ns"
      severity error;
    assert s'last_event = 0 ns report "s'last_event at 10 ns"
      severity error;
    assert not s'stable report "s'stable at 10 ns" severity error;
    assert s'delayed = '0' report "s'delayed in the event delta"
      severity error;
    wait for 0 ns;
    assert not s'event report "s'event one delta later"
      severity error;
    assert s'delayed = '1' report "s'delayed one delta later"
      severity error;
    assert s'stable report "s'stable one delta later" severity error;

    -- 15 ns: 5 ns after the event.
    wait for 5 ns;
    assert s'last_event = 5 ns report "s'last_event at 15 ns"
      severity error;
    assert s'last_value = '0' report "s'last_value at 15 ns"
      severity error;
    assert s'stable(4 ns) and not s'stable(6 ns)
      report "s'stable(t) at 15 ns" severity error;
    assert s'delayed(3 ns) = '1' and s'delayed(7 ns) = '0'
      report "s'delayed(t) at 15 ns" severity error;

    -- 20 ns: a transaction that is not an event.
    wait for 5 ns;
    assert s'active and not s'event report "s'active at 20 ns"
      severity error;
    assert s'last_active = 0 ns report "s'last_active at 20 ns"
      severity error;
    assert s'last_event = 10 ns report "s'last_event at 20 ns"
      severity error;
    assert s'stable(9 ns) report "s'stable(9 ns) at 20 ns"
      severity error;
    assert not s'quiet report "s'quiet at 20 ns" severity error;

    -- 25 ns: quiet for 5 ns, stable for 15 ns.
    wait for 5 ns;
    assert s'last_active = 5 ns report "s'last_active at 25 ns"
      severity error;
    assert s'quiet(4 ns) and not s'quiet(6 ns)
      report "s'quiet(t) at 25 ns" severity error;
    assert s'stable(14 ns) report "s'stable(14 ns) at 25 ns"
      severity error;

    -- 30 ns: the second event.
    wait for 5 ns;
    assert s'event and s'last_value = '1' report "s event at 30 ns"
      severity error;

    wait;
  end process attrs;

  stim : process is
  begin
    -- 20 ns: g glitches '1' -> '0' -> '1' across two deltas.
    wait for 20 ns;
    g <= '0';
    wait for 0 ns;
    g <= '1';

    -- 70 ns: d glitches '0' -> '1' -> '0' -> '1' across three deltas.
    wait for 50 ns;
    d <= '1';
    wait for 0 ns;
    d <= '0';
    wait for 0 ns;
    d <= '1';

    -- 80 ns: one plain event on d.
    wait for 10 ns;
    d <= '0';

    -- 90 ns: a 2 ns pulse on d, shorter than the 5 ns delay. 'delayed is
    -- a transport delay (LRM 16.2.3), so q_dly repeats it at 95-97 ns; an
    -- inertial `d after 5 ns` would swallow it.
    wait for 10 ns;
    d <= '1';
    wait for 2 ns;
    d <= '0';
    wait;
  end process stim;

  -- Events on q_dly during 75 ns. d'delayed(5 ns) is the process
  -- `R <= transport d after 5 ns` (LRM 16.2.3), so the three delta
  -- transactions of 70 ns all mature at 75 ns and each preempts the one
  -- before: one event, to the settled '1'.
  count_q75 : process (q_dly) is
  begin
    if now = 75 ns then
      q_ev75 <= q_ev75 + 1;
    end if;
  end process count_q75;

  check : process is
  begin
    -- 71 ns: the postponed process ran once at 70 ns (plus its initial
    -- run) and saw only the settled '1'; the ordinary process ran on
    -- every delta event.
    wait for 71 ns;
    assert p_runs = 2 report "postponed runs at 71 ns" severity error;
    assert p_seen = '1' report "postponed value at 71 ns"
      severity error;
    assert n_runs = 4 report "ordinary runs at 71 ns" severity error;
    wait for 3 ns;  -- 74 ns: d'delayed(5 ns) has not moved yet
    assert q_dly = '0' report "d'delayed(5 ns) at 74 ns"
      severity error;
    wait for 2 ns;  -- 76 ns: d'delayed(5 ns) settled at '1'
    assert q_dly = '1' report "d'delayed(5 ns) at 76 ns"
      severity error;
    -- The delayed copy moved exactly 5 ns after d (70 ns -> 75 ns).
    assert q_dly'last_event = 1 ns
      report "d'delayed(5 ns) rise not at 75 ns" severity error;
    wait for 5 ns;  -- 81 ns
    assert p_runs = 3 and p_seen = '0' report "postponed at 81 ns"
      severity error;
    assert n_runs = 5 report "ordinary runs at 81 ns" severity error;
    wait for 5 ns;  -- 86 ns
    assert q_dly = '0' report "d'delayed(5 ns) at 86 ns"
      severity error;
    assert q_dly'last_event = 1 ns
      report "d'delayed(5 ns) fall not at 85 ns" severity error;
    assert edges = 2 report "edges counted with 'last_value"
      severity error;
    assert g_lows = 1 report "ordinary process missed the glitch"
      severity error;
    assert n_tx = 3 report "s'transaction count" severity error;
    assert q_ev75 = 1 report "d'delayed(5 ns) events at 75 ns"
      severity error;
    wait for 10 ns;  -- 96 ns: the 2 ns pulse came through
    assert q_dly = '1' report "d'delayed(5 ns) dropped a short pulse"
      severity error;
    assert q_dly'last_event = 1 ns
      report "d'delayed(5 ns) pulse rise not at 95 ns" severity error;
    wait for 2 ns;  -- 98 ns
    assert q_dly = '0' and q_dly'last_event = 1 ns
      report "d'delayed(5 ns) pulse fall not at 97 ns" severity error;
    std.env.finish;
    wait;
  end process check;
end architecture tb;
