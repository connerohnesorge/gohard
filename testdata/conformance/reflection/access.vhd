-- ACCESS_VALUE_MIRROR and ACCESS_SUBTYPE_MIRROR.
use std.reflection.all;

entity access_tb is
end entity access_tb;

architecture sim of access_tb is
  type iptr is access integer;
begin
  check : process
    variable p   : iptr;
    variable np  : iptr;
    variable avm : access_value_mirror;
  begin
    p := new integer'(42);
    avm := p'reflect.to_access;
    report "null " & boolean'image(avm.is_null) & ", null pointer " &
      boolean'image(np'reflect.to_access.is_null) & ", subtype " &
      avm.get_subtype_mirror.simple_name & ", designated " &
      avm.get_subtype_mirror.designated_subtype.simple_name;
    assert p'reflect.get_value_class = CLASS_ACCESS
      report "class" severity failure;
    deallocate(p);
    wait;
  end process check;
end architecture sim;
