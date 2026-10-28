-- wait_forms_tb: every wait form plus inertial, transport and reject.
--
-- One stimulus process schedules all inputs at time 0 with transport
-- waveforms. Each checker process uses one wait form and asserts the
-- exact simulation time of every resumption; the final check at 500 ns
-- asserts how often each checker resumed.
library ieee;
use ieee.std_logic_1164.all;

entity wait_forms_tb is
end entity wait_forms_tb;

architecture tb of wait_forms_tb is
  type time_list is array (natural range <>) of time;
  type sl_list is array (natural range <>) of std_logic;
  type int_list is array (natural range <>) of integer;

  procedure expect_time (
    constant what : in    string;
    variable i    : inout natural;
    constant t    : in    time_list
  ) is
  begin
    assert i <= t'high and now = t(i)
      report what & ": resumed at an unexpected time" severity error;
    i := i + 1;
  end procedure expect_time;

  procedure expect_sl (
    constant what : in    string;
    variable i    : inout natural;
    constant t    : in    time_list;
    constant v    : in    sl_list;
    constant got  : in    std_logic
  ) is
  begin
    assert i <= t'high and now = t(i) and got = v(i)
      report what & ": unexpected event" severity error;
    i := i + 1;
  end procedure expect_sl;

  procedure expect_int (
    constant what : in    string;
    variable i    : inout natural;
    constant t    : in    time_list;
    constant v    : in    int_list;
    constant got  : in    integer
  ) is
  begin
    assert i <= t'high and now = t(i) and got = v(i)
      report what & ": unexpected event" severity error;
    i := i + 1;
  end procedure expect_int;

  signal a, b, c : std_logic := '0';
  signal x       : std_logic := '0';
  signal y_inert : std_logic;
  signal y_trans : std_logic;
  signal y_rej   : std_logic;
  signal ti      : integer   := 0;
  signal zd      : std_logic := '0';  -- proves wait for 0 ns is a delta
  signal ii      : integer   := 0;

  -- Resumption counts, written by the checkers.
  signal n_forever, n_on, n_on2, n_until, n_on_until : natural := 0;
  signal n_on_for, n_until_for, n_all, n_for, n_true : natural := 0;
  signal n_sens, n_edge                              : natural := 0;
  signal n_inert, n_trans, n_rej, n_ti, n_ii         : natural := 0;
begin
  dut : entity work.wait_forms
    port map (
      x       => x,
      y_inert => y_inert,
      y_trans => y_trans,
      y_rej   => y_rej
    );

  stim : process is
  begin
    a <= transport '1' after 10 ns, '0' after 30 ns, '1' after 60 ns;
    b <= transport '1' after 20 ns, '0' after 50 ns, '1' after 65 ns;
    c <= transport '1' after 40 ns, '0' after 70 ns, '1' after 80 ns;
    -- Pulses of 2 ns, 5 ns and 20 ns.
    x <= transport '1' after 100 ns, '0' after 102 ns,
         '1' after 110 ns, '0' after 115 ns,
         '1' after 130 ns, '0' after 150 ns;
    wait for 200 ns;
    -- Transport preemption: the later transaction at 220 ns is deleted,
    -- the one appended at 236 ns is kept.
    ti <= transport 1 after 20 ns;
    wait for 5 ns;
    ti <= transport 2 after 10 ns;
    wait for 1 ns;
    ti <= transport 3 after 30 ns;
    wait for 94 ns;
    -- Inertial preemption (300 ns): 2 and 3 are at or after the new
    -- transaction and 1 is inside the 8 ns rejection window: only 2 at
    -- 310 ns remains.
    ii <= 1 after 5 ns, 2 after 10 ns, 3 after 15 ns;
    wait for 2 ns;
    ii <= 2 after 8 ns;
    wait for 98 ns;
    -- Inertial marking (400 ns): 7 at 406 ns immediately precedes the
    -- new 7 at 409 ns with the same value and is kept; 5 is deleted.
    ii <= 5 after 3 ns, 7 after 6 ns;
    wait for 1 ns;
    ii <= 7 after 8 ns;
    wait;
  end process stim;

  -- wait;  runs once and never resumes.
  forever : process is
  begin
    n_forever <= n_forever + 1;
    wait;
  end process forever;

  -- wait on <one signal>;
  on_one : process is
    constant t : time_list := (10 ns, 30 ns, 60 ns);
    variable i : natural   := 0;
  begin
    for k in t'range loop
      wait on a;
      expect_time("wait on a", i, t);
      n_on <= i;
    end loop;
    wait;
  end process on_one;

  -- wait on <two signals>;
  on_two : process is
    constant t : time_list := (10 ns, 20 ns, 30 ns, 50 ns, 60 ns, 65 ns);
    variable i : natural   := 0;
  begin
    loop
      wait on a, b;
      expect_time("wait on a, b", i, t);
      n_on2 <= i;
    end loop;
  end process on_two;

  -- wait until <condition>;  an already true condition still needs a
  -- new event: the second wait skips the event at 70 ns (false) and
  -- resumes at 80 ns.
  until_only : process is
    constant t : time_list := (40 ns, 80 ns);
    variable i : natural   := 0;
  begin
    loop
      wait until c = '1';
      expect_time("wait until c = '1'", i, t);
      n_until <= i;
    end loop;
  end process until_only;

  -- wait on <signal> until <condition>;  the condition's signals are not
  -- in the sensitivity set.
  on_until : process is
    constant t : time_list := (30 ns, 65 ns);
    variable i : natural   := 0;
  begin
    wait on a until b = '1';
    expect_time("wait on a until b", i, t);
    n_on_until <= i;
    wait on b until a = '1';
    expect_time("wait on b until a", i, t);
    n_on_until <= i;
    wait;
  end process on_until;

  -- wait on <signal> for <time>;  first the timeout, then the event.
  on_for : process is
    constant t : time_list := (15 ns, 40 ns);
    variable i : natural   := 0;
  begin
    wait on c for 15 ns;
    expect_time("wait on c for 15 ns", i, t);
    n_on_for <= i;
    wait on c for 30 ns;
    expect_time("wait on c for 30 ns", i, t);
    n_on_for <= i;
    wait;
  end process on_for;

  -- wait until <condition> for <time>;  first the timeout (the event at
  -- 20 ns makes the condition false), then the condition.
  until_for : process is
    constant t : time_list := (25 ns, 50 ns);
    variable i : natural   := 0;
  begin
    wait until b = '0' for 25 ns;
    expect_time("wait until b for 25 ns", i, t);
    n_until_for <= i;
    wait until b = '0' for 50 ns;
    expect_time("wait until b for 50 ns", i, t);
    n_until_for <= i;
    wait;
  end process until_for;

  -- wait on <signal> until <condition> for <time>;  first the condition
  -- (third event on a), then the timeout.
  on_until_for : process is
    constant t : time_list := (60 ns, 72 ns);
    variable i : natural   := 0;
  begin
    wait on a until c = '1' for 100 ns;
    expect_time("wait on a until c for 100 ns", i, t);
    n_all <= i;
    wait on a until c = '1' for 12 ns;
    expect_time("wait on a until c for 12 ns", i, t);
    n_all <= i;
    wait;
  end process on_until_for;

  -- wait for <time>;  and wait for 0 ns (one delta, same time).
  for_only : process is
    constant t : time_list := (7 ns, 7 ns, 19 ns);
    variable i : natural   := 0;
  begin
    wait for 7 ns;
    expect_time("wait for 7 ns", i, t);
    zd <= '1';
    assert zd = '0' report "zd updated before any wait" severity error;
    wait for 0 ns;
    expect_time("wait for 0 ns", i, t);
    -- Exactly one delta passed: the update is visible, at the same now,
    -- and it happened in this very delta (no further delta elapsed).
    assert zd = '1' and zd'event
      report "wait for 0 ns: not exactly one delta" severity error;
    wait for 12 ns;
    expect_time("wait for 12 ns", i, t);
    n_for <= i;
    wait;
  end process for_only;

  -- wait until <condition without signals> for <time>;  the sensitivity
  -- set is empty, so only the timeout can resume the process.
  until_static : process is
    constant t : time_list := (3 ns, 6 ns);
    variable i : natural   := 0;
  begin
    for k in t'range loop
      wait until true for 3 ns;
      expect_time("wait until true for 3 ns", i, t);
      n_true <= i;
    end loop;
    wait;
  end process until_static;

  -- A sensitivity list is an implicit wait on at the end of the process.
  sens : process (c) is
    constant t : time_list := (0 ns, 40 ns, 70 ns, 80 ns);
    variable i : natural   := 0;
  begin
    expect_time("process (c)", i, t);
    n_sens <= i;
  end process sens;

  -- wait until rising_edge(<signal>);
  edge : process is
    constant t : time_list := (10 ns, 60 ns);
    variable i : natural   := 0;
  begin
    loop
      wait until rising_edge(a);
      expect_time("wait until rising_edge(a)", i, t);
      n_edge <= i;
    end loop;
  end process edge;

  -- Delay-line monitors. Each output leaves 'U' at 10 ns.
  mon_inert : process is
    constant t : time_list := (10 ns, 140 ns, 160 ns);
    constant v : sl_list   := ('0', '1', '0');
    variable i : natural   := 0;
  begin
    loop
      wait on y_inert;
      expect_sl("inertial 10 ns", i, t, v, y_inert);
      n_inert <= i;
    end loop;
  end process mon_inert;

  mon_trans : process is
    constant t : time_list :=
      (10 ns, 110 ns, 112 ns, 120 ns, 125 ns, 140 ns, 160 ns);
    constant v : sl_list := ('0', '1', '0', '1', '0', '1', '0');
    variable i : natural := 0;
  begin
    loop
      wait on y_trans;
      expect_sl("transport 10 ns", i, t, v, y_trans);
      n_trans <= i;
    end loop;
  end process mon_trans;

  mon_rej : process is
    constant t : time_list := (10 ns, 120 ns, 125 ns, 140 ns, 160 ns);
    constant v : sl_list   := ('0', '1', '0', '1', '0');
    variable i : natural   := 0;
  begin
    loop
      wait on y_rej;
      expect_sl("reject 3 ns inertial 10 ns", i, t, v, y_rej);
      n_rej <= i;
    end loop;
  end process mon_rej;

  mon_ti : process is
    constant t : time_list := (215 ns, 236 ns);
    constant v : int_list  := (2, 3);
    variable i : natural   := 0;
  begin
    loop
      wait on ti;
      expect_int("transport preemption", i, t, v, ti);
      n_ti <= i;
    end loop;
  end process mon_ti;

  mon_ii : process is
    constant t : time_list := (310 ns, 406 ns);
    constant v : int_list  := (2, 7);
    variable i : natural   := 0;
  begin
    loop
      wait on ii;
      expect_int("inertial preemption", i, t, v, ii);
      n_ii <= i;
    end loop;
  end process mon_ii;

  final : process is
  begin
    wait for 500 ns;
    assert n_forever = 1 report "wait; resumed" severity error;
    assert n_on = 3 report "wait on a count" severity error;
    assert n_on2 = 6 report "wait on a, b count" severity error;
    assert n_until = 2 report "wait until count" severity error;
    assert n_on_until = 2 report "wait on until count" severity error;
    assert n_on_for = 2 report "wait on for count" severity error;
    assert n_until_for = 2 report "wait until for count"
      severity error;
    assert n_all = 2 report "wait on until for count" severity error;
    assert n_for = 3 report "wait for count" severity error;
    assert n_true = 2 report "wait until true for count"
      severity error;
    assert n_sens = 4 report "sensitivity list count" severity error;
    assert n_edge = 2 report "rising_edge count" severity error;
    assert n_inert = 3 report "inertial event count" severity error;
    assert n_trans = 7 report "transport event count" severity error;
    assert n_rej = 5 report "reject event count" severity error;
    assert n_ti = 2 report "transport preemption count"
      severity error;
    assert n_ii = 2 report "inertial preemption count" severity error;
    std.env.stop;
    wait;
  end process final;
end architecture tb;
