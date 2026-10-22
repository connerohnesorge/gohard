-- std: 2008
-- A selected signal assignment with range choices, concurrent and
-- sequential (LRM 10.5.4, 11.6).
entity selected_range_tb is
end entity selected_range_tb;

architecture sim of selected_range_tb is
  signal sel : integer := 2;
  signal a, b : bit;
begin
  with sel select a <= '1' when 0 to 3, '0' when others;

  process
  begin
    with sel select b <= '1' when 4 to 7 | 9, '0' when others;
    wait for 1 ns;
    report "a " & bit'image(a) & " b " & bit'image(b);
    assert a = '1' and b = '0' severity failure;
    sel <= 9;
    wait for 1 ns;
    with sel select b <= '1' when 4 to 7 | 9, '0' when others;
    wait for 1 ns;
    report "a " & bit'image(a) & " b " & bit'image(b);
    assert a = '0' and b = '1' severity failure;
    wait;
  end process;
end architecture sim;
