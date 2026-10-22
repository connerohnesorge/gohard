-- An aggregate with others assigned to an object whose constraint is
-- inferred from its initial value (LRM 6.4.2.3, 9.3.3.3): the
-- object's constraint is the aggregate's context, for a variable and
-- a signal.
entity others_target_tb is
end entity others_target_tb;

architecture sim of others_target_tb is
  signal sv : bit_vector := "1010";
begin
  process
    variable v : integer_vector := (1, 2, 3, 4, 5);
    variable s : string := "hello";
  begin
    v := (others => 9);
    s := (1 => 'q', others => 'z');
    sv <= (others => '0');
    wait for 1 ns;
    report integer'image(v'length) & " " & integer'image(v(4)) & " " &
      s & " " & to_string(sv);
    assert v(1) = 9 and s = "qzzzz" and sv = "0000" severity failure;
    wait;
  end process;
end architecture sim;
