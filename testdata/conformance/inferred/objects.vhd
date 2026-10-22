-- Inferred constraints (VHDL-2019, LRM 6.4.2.1): an object of an
-- unconstrained or partially constrained subtype with an initial
-- value takes its index ranges from the value, for variables and
-- signals, nested element constraints included.
entity inferred_tb is
end entity inferred_tb;

architecture sim of inferred_tb is
  type word_array is array (natural range <>) of bit_vector;

  signal s : bit_vector := "1010";
  signal w : word_array := ("001", "010", "100", "111");
  signal r : string := "hello";
begin
  check : process
    variable inf : bit_vector := "10110";
    variable str : string := "abc";
    variable wa  : word_array := (x"0f", x"f0");
  begin
    report "inf'length = " & integer'image(inf'length) & ", str = " &
      integer'image(str'left) & " to " & integer'image(str'right);
    report "wa = " & integer'image(wa'length) & " x " &
      integer'image(wa(0)'length);
    report "s'length = " & integer'image(s'length) & ", w = " &
      integer'image(w'length) & " x " & integer'image(w(1)'length) &
      ", r'right = " & integer'image(r'right);
    assert inf'length = 5 and wa(1)'length = 8 report "variables"
      severity error;
    assert s'length = 4 and w(3)'length = 3 report "signals"
      severity error;
    s <= "0101";
    wait for 1 ns;
    report "s = " & to_string(s);
    assert s = "0101" report "assignment" severity error;
    wait;
  end process check;
end architecture sim;
