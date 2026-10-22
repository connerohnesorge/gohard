-- nvc-differs: NVC 1.23 cannot elaborate a call instance whose generic actual is a variable ("missing body for ...$inst")
-- The actuals of a call's generic map (VHDL-2019, LRM 9.3.4) are
-- evaluated at each call: one call site runs with other generics
-- each time (a loop parameter, a variable), and memoized calls of
-- the pure function keep them apart.
entity call_generic_map_dynamic_tb is
end entity call_generic_map_dynamic_tb;

architecture sim of call_generic_map_dynamic_tb is
  function scale
    generic (k : integer)
    parameter (x : integer) return integer is
  begin
    return k * x;
  end function scale;

  procedure accumulate
    generic (step : integer)
    parameter (variable acc : inout integer) is
  begin
    acc := acc + step;
  end procedure accumulate;
begin
  p : process
    variable s   : integer := 0;
    variable kk  : integer := 0;
    variable acc : integer := 0;
  begin
    for i in 1 to 4 loop
      s := s + scale generic map (k => i) (x => 10);
      kk := 10 * i;
      accumulate generic map (step => kk) parameter map (acc);
    end loop;
    report "sum of scale (k => 1 .. 4) (10) = " & integer'image(s);
    report "acc = " & integer'image(acc);
    for i in 1 to 2 loop
      s := scale generic map (k => i) (x => 3) +
        scale generic map (k => i + 1) (x => 3);
      report "i = " & integer'image(i) & ": " & integer'image(s);
    end loop;
    assert s = 15 and acc = 100
      report "dynamic generic maps" severity failure;
    wait;
  end process p;
end architecture sim;
