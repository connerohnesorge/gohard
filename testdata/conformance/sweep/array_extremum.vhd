-- std: 2008
-- The VHDL-2008 MINIMUM and MAXIMUM of a one-dimensional array of a
-- scalar type (LRM 5.3.2.4): over INTEGER_VECTOR, STRING, a user
-- array, REAL_VECTOR, and of a null array the bound of the element
-- subtype.
entity array_extremum_tb is
end entity array_extremum_tb;

architecture sim of array_extremum_tb is
  type small is range 0 to 100;
  type small_vector is array (natural range <>) of small;
begin
  process
    variable iv : integer_vector(1 to 3) := (5, 2, 6);
    variable sv : small_vector(0 to 2) := (9, 3, 7);
    variable rv : real_vector(0 to 1) := (1.5, -2.0);
    variable nv : small_vector(1 to 0);
  begin
    report "integer " & integer'image(minimum(iv)) & " " &
      integer'image(maximum(iv));
    report "small " & small'image(minimum(sv)) & " " &
      small'image(maximum(sv));
    report "string " & character'image(minimum(string'("hello"))) &
      " " & character'image(maximum(string'("hello")));
    report "null " & small'image(minimum(nv)) & " " &
      small'image(maximum(nv));
    assert minimum(rv) = -2.0 and maximum(rv) = 1.5 severity failure;
    wait;
  end process;
end architecture sim;
