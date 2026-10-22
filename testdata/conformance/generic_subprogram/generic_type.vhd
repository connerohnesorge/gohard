-- Generic types of subprogram instances (LRM 6.5.3, 9.3.4): the body
-- runs with the actual subtype, in a pure function, in a procedure and
-- in a call with a generic map; 'IMAGE, 'HIGH (of a discrete generic
-- type) and objects of the generic type follow the actual.
entity generic_type_tb is
end entity generic_type_tb;

architecture sim of generic_type_tb is
  type color is (red, green, blue);

  function ident
    generic (type t)
    parameter (x : t) return t is
    variable tmp : t;
  begin
    tmp := x;
    return tmp;
  end function ident;

  function describe
    generic (type t is (<>))
    parameter (x : t) return string is
  begin
    return t'image(x) & " of " & t'image(t'high);
  end function describe;

  procedure swap
    generic (type t)
    parameter (variable a, b : inout t) is
    variable tmp : t;
  begin
    tmp := a;
    a := b;
    b := tmp;
  end procedure swap;

  function ident_color is new ident generic map (t => color);
  procedure swap_int is new swap generic map (t => integer);
begin
  p : process
    variable i, j : integer := 0;
    variable r, q : real := 0.0;
  begin
    report "ident(true) = " &
      boolean'image(ident generic map (t => boolean) (true));
    report "ident_color(green) = " & color'image(ident_color(green));
    report describe generic map (t => color) (green);
    report describe generic map (t => integer) (5);
    i := 1;
    j := 2;
    swap_int(i, j);
    r := 1.5;
    q := 2.5;
    swap generic map (t => real) parameter map (r, q);
    report "swapped " & integer'image(i) & " " & integer'image(j) &
      " " & real'image(r) & " " & real'image(q);
    assert ident generic map (t => integer) (7) = 7 and
      ident_color(blue) = blue and i = 2 and j = 1 and r = 2.5
      report "generic types" severity failure;
    wait;
  end process p;
end architecture sim;
