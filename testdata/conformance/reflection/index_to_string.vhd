-- The implicit operations of std.reflection's INDEX (an integer
-- subtype) and INDEX_VECTOR: TO_STRING of the lengths and bounds the
-- mirrors return, MAXIMUM and MINIMUM.
use std.reflection.all;

entity index_to_string_tb is
end entity index_to_string_tb;

architecture sim of index_to_string_tb is
  type grid is array (natural range <>, natural range <>) of bit;
begin
  process
    variable x  : index := 3;
    variable iv : index_vector(1 to 2) := (5, 6);
    variable g  : grid(0 to 2, 1 to 4);
    variable am : array_subtype_mirror;
  begin
    am := g'reflect.to_array.get_subtype_mirror;
    report "index " & to_string(x) & " " & to_string(iv(1)) & " " &
      to_string(maximum(x, iv(2)));
    report "grid dims " & to_string(am.dimensions) & " len2 " &
      to_string(am.length(2)) & " left2 " & to_string(am.left(2));
    report "vector " & to_string(iv);
    assert to_string(am.length(1)) = "3" severity failure;
    wait;
  end process;
end architecture sim;
