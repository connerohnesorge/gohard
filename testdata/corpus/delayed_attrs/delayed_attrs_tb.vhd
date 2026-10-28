-- delayed_attrs_tb: 'delayed(t) of a vector and of a resolved signal
-- with two drivers (#63). S'DELAYED(T) is the signal S delayed by a
-- transport delay of T (LRM 16.2.3), so each element of the vector v,
-- and the resolved value of r, reappear 5 ns later, short pulses
-- included. The testbench is the whole design; it ends with
-- std.env.finish at 50 ns.
--   v:  "0000", "1010" at 10 ns, element 0 set at 12 ns ("1011"),
--       "0000" at 14 ns, a 1 ns pulse on element 3 at 20 ns
--   r:  'Z', '1' from drv_a at 10 ns, 'X' with '0' from drv_b at
--       20 ns, '0' at 30 ns, 'Z' at 40 ns
library ieee;
use ieee.std_logic_1164.all;

entity delayed_attrs_tb is
end entity delayed_attrs_tb;

architecture tb of delayed_attrs_tb is
  signal v     : std_logic_vector(3 downto 0) := "0000";
  signal r     : std_logic := 'Z';
  signal v_dly : std_logic_vector(3 downto 0);
  signal r_dly : std_logic;
begin
  v_dly <= v'delayed(5 ns);
  r_dly <= r'delayed(5 ns);

  stim_v : process is
  begin
    wait for 10 ns;
    v <= "1010";
    wait for 2 ns;
    v(0) <= '1';
    wait for 2 ns;
    v <= "0000";
    wait for 6 ns;
    v(3) <= '1';
    wait for 1 ns;
    v(3) <= '0';
    wait;
  end process stim_v;

  drv_a : process is
  begin
    r <= 'Z';
    wait for 10 ns;
    r <= '1';
    wait for 20 ns;
    r <= 'Z';
    wait;
  end process drv_a;

  drv_b : process is
  begin
    r <= 'Z';
    wait for 20 ns;
    r <= '0';
    wait for 20 ns;
    r <= 'Z';
    wait;
  end process drv_b;

  check : process is
  begin
    wait for 12 ns;  -- 12 ns: nothing delayed has moved yet
    assert v_dly = "0000" and r_dly = 'Z' report "delayed at 12 ns"
      severity error;
    wait for 4 ns;   -- 16 ns
    assert v_dly = "1010" report "v delayed at 16 ns" severity error;
    assert r_dly = '1' report "r delayed at 16 ns" severity error;
    wait for 2 ns;   -- 18 ns: one element moved
    assert v_dly = "1011" and v'delayed(5 ns)(0) = '1'
      report "v delayed at 18 ns" severity error;
    assert v_dly'last_event = 1 ns report "v delayed event at 17 ns"
      severity error;
    wait for 2 ns;   -- 20 ns
    assert v_dly = "0000" report "v delayed at 20 ns" severity error;
    wait for 5.5 ns; -- 25.5 ns: the pulse on element 3
    assert v_dly = "1000" and v'delayed(5 ns)(2 downto 0) = "000"
      report "v delayed pulse at 25.5 ns" severity error;
    wait for 1.5 ns; -- 27 ns: the drivers disagree from 20 ns on
    assert v_dly = "0000" report "v delayed at 27 ns" severity error;
    assert r_dly = 'X' report "r delayed at 27 ns" severity error;
    wait for 10 ns;  -- 37 ns
    assert r_dly = '0' report "r delayed at 37 ns" severity error;
    wait for 10 ns;  -- 47 ns
    assert r_dly = 'Z' and r'delayed(5 ns) = 'Z'
      report "r delayed at 47 ns" severity error;
    wait for 3 ns;   -- 50 ns
    std.env.finish;
    wait;
  end process check;
end architecture tb;
