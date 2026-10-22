-- ARRAY_VALUE_MIRROR and ARRAY_SUBTYPE_MIRROR: bounds, index and
-- element subtypes, elements of one and two dimensions.
use std.reflection.all;

entity array_tb is
end entity array_tb;

architecture sim of array_tb is
  type color is (red, green, blue);
  subtype small is integer range -3 to 10;
  type mat is array (1 to 2, color range red to green) of small;
begin
  check : process
    variable bv  : bit_vector(3 downto 0) := "1010";
    variable s   : string(1 to 3) := "abc";
    variable mm  : mat := ((1, 2), (3, 4));
    variable asm : array_subtype_mirror;
  begin
    asm := bv'reflect.to_array.get_subtype_mirror;
    report asm.simple_name & ": dimensions " & index'image(asm.dimensions) &
      ", left " & index'image(asm.left) & ", right " &
      index'image(asm.right) & ", low " & index'image(asm.low) &
      ", high " & index'image(asm.high) & ", length " &
      index'image(asm.length) & ", ascending " &
      boolean'image(asm.ascending) & ", element " &
      asm.element_subtype.simple_name & ", index " &
      asm.index_subtype.simple_name;
    report s'reflect.get_subtype_mirror.simple_name & ": " &
      s'reflect.to_array.get(1).to_enumeration.image &
      s'reflect.to_array.get(3).to_enumeration.image;
    asm := mm'reflect.to_array.get_subtype_mirror;
    report asm.simple_name & ": dimensions " & index'image(asm.dimensions) &
      ", left 2 " & index'image(asm.left(2)) & ", right 2 " &
      index'image(asm.right(2)) & ", length 2 " & index'image(asm.length(2)) &
      ", index 2 " & asm.index_subtype(2).simple_name & ", element " &
      asm.element_subtype.simple_name;
    report "mm(2, green) = " & mm'reflect.to_array.get(2, 1).to_integer.image &
      ", mm(2, red) = " & mm'reflect.to_array.get((2, 0)).to_integer.image;
    wait;
  end process check;
end architecture sim;
