-- md_resolve_tb: self-checking testbench for md_resolve.
library ieee;
use ieee.std_logic_1164.all;

entity md_resolve_tb is
end entity md_resolve_tb;

architecture tb of md_resolve_tb is
  type sl_table is array (std_ulogic, std_ulogic) of std_ulogic;
  type sl_list is array (natural range <>) of std_ulogic;

  -- The IEEE 1164 resolution table, written out independently.
  constant expect : sl_table := (
    --  U    X    0    1    Z    W    L    H    -
    ('U', 'U', 'U', 'U', 'U', 'U', 'U', 'U', 'U'),  -- U
    ('U', 'X', 'X', 'X', 'X', 'X', 'X', 'X', 'X'),  -- X
    ('U', 'X', '0', 'X', '0', '0', '0', '0', 'X'),  -- 0
    ('U', 'X', 'X', '1', '1', '1', '1', '1', 'X'),  -- 1
    ('U', 'X', '0', '1', 'Z', 'W', 'L', 'H', 'X'),  -- Z
    ('U', 'X', '0', '1', 'W', 'W', 'W', 'W', 'X'),  -- W
    ('U', 'X', '0', '1', 'L', 'W', 'L', 'W', 'X'),  -- L
    ('U', 'X', '0', '1', 'H', 'W', 'W', 'H', 'X'),  -- H
    ('U', 'X', 'X', 'X', 'X', 'X', 'X', 'X', 'X')   -- -
  );

  -- All nine values, so every one of the 81 table entries is read.
  constant probe : sl_list :=
    ('U', 'X', '0', '1', 'Z', 'W', 'L', 'H', '-');

  signal a_en   : std_logic := '0';
  signal a_val  : std_logic := 'Z';
  signal b_en   : std_logic := '0';
  signal b_val  : std_logic := 'Z';
  signal t_val  : std_logic := 'Z';
  signal bus_io : std_logic;
  signal v_en   : std_logic := '0';
  signal t_vec  : std_logic_vector(3 downto 0) := (others => 'Z');
  signal vec_io : std_logic_vector(3 downto 0);
  signal d0     : std_logic := '0';
  signal d3     : std_logic;
  signal swap   : std_logic := '0';
  signal p_out  : std_logic;
  signal q_out  : std_logic;
begin
  dut : entity work.md_resolve
    port map (
      a_en   => a_en,
      a_val  => a_val,
      b_en   => b_en,
      b_val  => b_val,
      bus_io => bus_io,
      v_en   => v_en,
      vec_io => vec_io,
      d0     => d0,
      d3     => d3,
      swap   => swap,
      p_out  => p_out,
      q_out  => q_out
    );

  -- Third driver of bus_io and second driver of vec_io.
  bus_io <= t_val;
  vec_io <= t_vec;

  stim : process is
  begin
    wait for 10 ns;

    -- 1. Two drivers inside the DUT, testbench driver released.
    a_en <= '1';
    b_en <= '1';
    for i in probe'range loop
      for j in probe'range loop
        a_val <= probe(i);
        b_val <= probe(j);
        wait for 1 ns;
        assert bus_io = expect(probe(i), probe(j))
          report "two-driver resolution mismatch" severity error;
      end loop;
    end loop;

    -- 2. Three drivers: two in the DUT, one in the testbench.
    a_val <= 'L';
    b_val <= 'H';
    t_val <= 'Z';
    wait for 1 ns;
    assert bus_io = 'W' report "L,H,Z must give W" severity error;
    t_val <= '1';
    wait for 1 ns;
    assert bus_io = '1' report "L,H,1 must give 1" severity error;
    a_val <= '0';
    wait for 1 ns;
    assert bus_io = 'X' report "0,H,1 must give X" severity error;

    -- 3. All drivers released: a resolved net with only Z drivers.
    a_en  <= '0';
    b_en  <= '0';
    t_val <= 'Z';
    wait for 1 ns;
    assert bus_io = 'Z' report "all Z must give Z" severity error;

    -- 4. Element-wise vector resolution.
    v_en  <= '1';
    t_vec <= "1L0Z";
    wait for 1 ns;
    assert vec_io = "X10Z" report "vector resolution mismatch"
      severity error;
    v_en <= '0';
    wait for 1 ns;
    assert vec_io = "1L0Z" report "vector after release mismatch"
      severity error;

    -- 5. Delta ordering through the chain d0 -> d1 -> d2 -> d3.
    --    The DUT ports add no delta, so d3 trails d0 by three deltas.
    d0 <= '1';
    wait for 0 ns;  -- delta 1: d0 updated
    assert d0 = '1' and d3 = '0' report "delta 1 ordering"
      severity error;
    wait for 0 ns;  -- delta 2: d1 updated
    assert d3 = '0' report "delta 2 ordering" severity error;
    wait for 0 ns;  -- delta 3: d2 updated
    assert d3 = '0' report "delta 3 ordering" severity error;
    wait for 0 ns;  -- delta 4: d3 updated
    assert d3 = '1' report "delta 4 ordering" severity error;

    -- 6. Swap in one delta: both assignments read the old values.
    wait for 1 ns;
    assert p_out = '0' and q_out = '1' report "swap initial values"
      severity error;
    swap <= '1';
    wait for 1 ns;
    assert p_out = '1' and q_out = '0' report "swap mismatch"
      severity error;

    std.env.finish;
    wait;
  end process stim;
end architecture tb;
