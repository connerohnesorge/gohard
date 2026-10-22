-- Generic maps on subprogram calls (VHDL-2019, LRM 9.3.4): each call
-- is an implicit instance of the uninstantiated subprogram, in a
-- function or a procedure call, with named or positional generic
-- associations and a default.
entity call_generic_map_tb is
end entity call_generic_map_tb;

architecture sim of call_generic_map_tb is
  function scale
    generic (k : integer)
    parameter (x : integer) return integer is
  begin
    return k * x;
  end function scale;

  procedure accumulate
    generic (step : integer := 1)
    parameter (variable acc : inout integer; n : in natural) is
  begin
    for i in 1 to n loop
      acc := acc + step;
    end loop;
  end procedure accumulate;

  constant c : integer := scale generic map (k => 5) (x => 7);
begin
  p : process
    variable acc : integer := 0;
    variable s   : integer := 0;
  begin
    assert scale generic map (k => 3) (x => 4) = 12
      report "generic map on subprogram call" severity failure;
    report "scale generic map (k => 3) (x => 4) = " &
      integer'image(scale generic map (k => 3) (x => 4));
    report "c = " & integer'image(c);
    s := scale generic map (k => 2) (10) + scale generic map (4) (10);
    report "scale (k => 2) (10) + scale (4) (10) = " & integer'image(s);
    accumulate generic map (step => 5) parameter map (acc, 2);
    accumulate generic map (step => open) parameter map (acc, 3);
    report "acc = " & integer'image(acc);
    assert c = 35 and s = 60 and acc = 13
      report "call generic maps" severity failure;
    wait;
  end process p;
end architecture sim;
