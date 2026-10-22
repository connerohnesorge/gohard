-- Conditional returns run by the simulator (VHDL-2019, LRM 10.13):
-- impure functions and procedures, whose calls are executed rather
-- than folded, with conditions on values known only at run time, an
-- unaffected branch, and a return when inside a loop.
entity runtime_tb is
end entity runtime_tb;

architecture sim of runtime_tb is
  signal limit : integer := 9;

  impure function cap (x : integer) return integer is
  begin
    return limit when x > limit else unaffected;
    return x;
  end function cap;

  impure function pick (x : integer) return integer is
  begin
    return -1 when x < 0 else limit when x > limit;
    return x * 2;
  end function pick;

  procedure first_over (v : in integer; n : out integer) is
  begin
    n := 0;
    for k in 1 to 100 loop
      n := k;
      return when k * v > limit;
    end loop;
    n := -1;
  end procedure first_over;
begin
  p : process
    variable n : integer;
    variable s : integer := 0;
  begin
    for i in -2 to 12 loop
      s := s + cap(i);
    end loop;
    report "cap sum = " & integer'image(s);
    report "pick = " & integer'image(pick(-3)) & " " &
      integer'image(pick(4)) & " " & integer'image(pick(30));
    for v in 0 to 4 loop
      first_over(v, n);
      report "first_over(" & integer'image(v) & ") = " & integer'image(n);
    end loop;
    wait;
  end process p;
end architecture sim;
