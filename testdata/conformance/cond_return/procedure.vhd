-- Conditional return in procedures (VHDL-2019, LRM 10.13): return
-- when a condition holds, so the rest of the body is skipped.
entity procedure_tb is
end entity procedure_tb;

architecture sim of procedure_tb is
  procedure halve (variable x : inout integer; variable n : out natural)
  is
  begin
    n := 0;
    for i in 1 to 10 loop
      return when x mod 2 /= 0;
      x := x / 2;
      n := i;
    end loop;
  end procedure halve;
begin
  p : process
    variable x : integer := 24;
    variable n : natural;
  begin
    halve(x, n);
    report "24: x = " & integer'image(x) & ", n = " & integer'image(n);
    x := 7;
    halve(x, n);
    report "7: x = " & integer'image(x) & ", n = " & integer'image(n);
    assert x = 7 and n = 0 report "halve" severity error;
    wait;
  end process p;
end architecture sim;
