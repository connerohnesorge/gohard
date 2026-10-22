-- Conditional return in functions (VHDL-2019, LRM 10.13): a return
-- with a condition, a conditional value and an unaffected branch.
entity function_tb is
end entity function_tb;

architecture sim of function_tb is
  function clip (x : integer) return integer is
  begin
    return 0 when x < 0;
    return x;
  end function clip;

  function sign (x : integer) return integer is
  begin
    return -1 when x < 0 else 1 when x > 0 else 0;
  end function sign;

  function cap (x, lim : integer) return integer is
  begin
    return lim when x > lim else unaffected;
    return x;
  end function cap;

  constant c : integer := clip(-5) + cap(20, 9);
begin
  p : process
    variable i : integer := -5;
  begin
    report "clip(-5) = " & integer'image(clip(i));
    report "clip(3) = " & integer'image(clip(3));
    report "sign = " & integer'image(sign(i)) & integer'image(sign(0)) &
      integer'image(sign(4));
    report "cap = " & integer'image(cap(20, 9)) & " " &
      integer'image(cap(4, 9));
    report "c = " & integer'image(c);
    assert clip(i) = 0 and clip(3) = 3 report "clip" severity error;
    assert cap(4, 9) = 4 report "cap" severity error;
    wait;
  end process p;
end architecture sim;
