-- An open index constraint with a constrained element, arr(open)(5
-- downto 3), leaves the index range to the value (inferred
-- constraints, LRM 5.3.2.2, 6.4.2.1) for variables, signals and
-- constants, and to a later index constraint.
entity open_index_tb is
end entity open_index_tb;

architecture sim of open_index_tb is
  type arr is array (natural range <>) of bit_vector;
  subtype parr is arr(open)(5 downto 3);
  subtype parr2 is parr(1 to 2);

  constant C : parr := ("101", "011", "110");
  signal s : parr := ("101", "011", "110", "000");
begin
  process
    variable v : parr := ("101", "011");
    variable w : parr2;
  begin
    report "v " & integer'image(v'length) & " " & integer'image(v'left) &
      " " & integer'image(v(1)'left) & " " & integer'image(v(1)'right);
    report "w " & integer'image(w'left) & " " & integer'image(w'right) &
      " " & integer'image(w(2)'left);
    report "C " & integer'image(C'length) & " " & integer'image(C(2)'low);
    report "s " & integer'image(s'length) & " " & integer'image(s'right) &
      " " & integer'image(s(0)'high);
    v := ("111", "000");
    report "v(0) " & to_string(v(0));
    wait;
  end process;
end architecture sim;
