-- 'REFLECT of an integer object and subtype: INTEGER_VALUE_MIRROR and
-- INTEGER_SUBTYPE_MIRROR, and the generic mirrors.
use std.reflection.all;

entity integer_tb is
end entity integer_tb;

architecture sim of integer_tb is
  subtype small is integer range -3 to 10;
begin
  check : process
    variable i   : small := 7;
    variable vm  : value_mirror;
    variable ivm : integer_value_mirror;
    variable ism : integer_subtype_mirror;
  begin
    vm := i'reflect;
    report "class " & value_class'image(vm.get_value_class);
    ivm := vm.to_integer;
    report "value " & integer'image(ivm.value) & ", image " & ivm.image;
    ism := ivm.get_subtype_mirror;
    report ism.simple_name & ": left " & ism.left.image & ", right " &
      ism.right.image & ", low " & ism.low.image & ", high " &
      ism.high.image & ", length " & index'image(ism.length) &
      ", ascending " & boolean'image(ism.ascending);
    report "subtype " & vm.get_subtype_mirror.simple_name & " of class " &
      type_class'image(vm.get_subtype_mirror.get_type_class);
    report "type marks " & integer'reflect.simple_name & ", " &
      natural'reflect.to_integer.simple_name & ", " &
      small'reflect.to_integer.high.image;
    assert ivm.to_value_mirror = vm report "to_value_mirror" severity failure;
    assert ism.to_subtype_mirror.get_type_class = CLASS_INTEGER
      report "to_subtype_mirror" severity failure;
    wait;
  end process check;
end architecture sim;
