entity forms is
end entity forms;

architecture sim of forms is
  signal s, t : integer := 0;
  signal u    : bit     := '0';

  impure function bump return integer is
  begin
    return s + 1;
  end function bump;

  function sq (x : integer) return integer is
  begin
    if x < 0 then
      return -x * x;
    end if;
    return x * x;
  end function sq;

  procedure classify (x : in integer; r : out integer) is
  begin
    if x > 2 then
      r := 2;
    elsif x > 0 then
      r := 1;
    else
      r := 0;
    end if;
  end procedure classify;

  procedure check (signal x : in integer) is
  begin
    assert x >= -1 report "low";
  end procedure check;
begin
  p : process
    variable v, w : integer := 0;
  begin
    for i in 0 to 3 loop
      w := sq(i - 1);
      classify(i, v);
      w := 1 when v = 1 else 2 when v = 2;
      with v select w := 10 when 0, 20 when others;
      t <= v when v > 0 else -1;
      with v select u <= '1' when 2, '0' when others;
      next when i = 1;
      s <= bump;
      wait for 1 ns;
    end loop;
    wait;
  end process p;

  assert s < 100 report "big";
  check(t);
end architecture sim;
