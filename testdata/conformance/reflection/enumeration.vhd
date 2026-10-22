-- ENUMERATION_VALUE_MIRROR and ENUMERATION_SUBTYPE_MIRROR: positions,
-- images, literals by position and by image.
use std.reflection.all;

entity enumeration_tb is
end entity enumeration_tb;

architecture sim of enumeration_tb is
  type color is (red, green, blue);
begin
  check : process
    variable c   : color := green;
    variable evm : enumeration_value_mirror;
    variable esm : enumeration_subtype_mirror;
  begin
    evm := c'reflect.to_enumeration;
    esm := evm.get_subtype_mirror;
    report "pos " & integer'image(evm.pos) & ", image " & evm.image;
    report esm.simple_name & ": length " & index'image(esm.length) &
      ", left " & esm.left.image & ", right " & esm.right.image &
      ", low " & esm.low.image & ", high " & esm.high.image &
      ", ascending " & boolean'image(esm.ascending);
    report "literal 1 " & esm.enumeration_literal(1).image &
      ", blue at " & integer'image(esm.enumeration_literal("blue").pos);
    report "character 65 " &
      character'reflect.to_enumeration.enumeration_literal(65).image &
      ", bit '1' at " & integer'image(
        bit'reflect.to_enumeration.enumeration_literal("'1'").pos);
    assert c'reflect.get_value_class = CLASS_ENUMERATION
      report "class" severity failure;
    wait;
  end process check;
end architecture sim;
