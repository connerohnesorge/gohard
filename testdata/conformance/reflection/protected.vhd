-- PROTECTED_VALUE_MIRROR and PROTECTED_SUBTYPE_MIRROR of a shared
-- variable and of the type.
use std.reflection.all;

entity protected_tb is
end entity protected_tb;

architecture sim of protected_tb is
  type pt is protected
    procedure inc;
  end protected pt;

  type pt is protected body
    variable n : integer := 0;

    procedure inc is
    begin
      n := n + 1;
    end procedure inc;
  end protected body pt;

  shared variable cnt : pt;
begin
  check : process
  begin
    report "class " & value_class'image(cnt'reflect.get_value_class) &
      ", subtype " &
      cnt'reflect.to_protected.get_subtype_mirror.simple_name & ", type " &
      pt'reflect.simple_name & " of class " &
      type_class'image(pt'reflect.get_type_class);
    assert cnt'reflect.to_protected.to_value_mirror.get_value_class =
      CLASS_PROTECTED report "to_value_mirror" severity failure;
    wait;
  end process check;
end architecture sim;
