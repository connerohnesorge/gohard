-- FLOATING_VALUE_MIRROR and FLOATING_SUBTYPE_MIRROR.
use std.reflection.all;

entity floating_tb is
end entity floating_tb;

architecture sim of floating_tb is
begin
  check : process
    variable r   : real := 2.5;
    variable fvm : floating_value_mirror;
    variable fsm : floating_subtype_mirror;
  begin
    fvm := r'reflect.to_floating;
    fsm := fvm.get_subtype_mirror;
    report "value " & real'image(fvm.value) & ", image " & fvm.image;
    report fsm.simple_name & ": ascending " & boolean'image(fsm.ascending) &
      ", low is real'low " & boolean'image(fsm.low.value = real'low) &
      ", right is real'high " & boolean'image(fsm.right.value = real'high);
    assert r'reflect.get_value_class = CLASS_FLOATING
      report "class" severity failure;
    wait;
  end process check;
end architecture sim;
