-- race_order_tb: raises go at 10 ns and sample at 15 ns, and checks at
-- 20 ns that order holds each of 1 .. 8 exactly once. Which permutation
-- it is depends on the in-delta order; that every writer ran once does
-- not. Ends with std.env.finish.
entity race_order_tb is
end entity race_order_tb;

architecture sim of race_order_tb is
  signal go     : bit := '0';
  signal sample : bit := '0';
  signal order  : natural;
begin
  dut : entity work.race_order
    port map (go => go, sample => sample, order => order);

  check : process is
    variable v    : natural;
    variable seen : bit_vector(1 to 8) := (others => '0');
  begin
    go     <= '1' after 10 ns;
    sample <= '1' after 15 ns;
    wait for 20 ns;
    v := order;
    for k in 1 to 8 loop
      assert v mod 10 >= 1 and v mod 10 <= 8 and seen(v mod 10) = '0'
        report "order is not a permutation of 1 to 8" severity failure;
      seen(v mod 10) := '1';
      v := v / 10;
    end loop;
    assert v = 0 report "order has more than 8 digits" severity failure;
    std.env.finish;
    wait;
  end process check;
end architecture sim;
