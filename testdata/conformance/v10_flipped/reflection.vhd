-- The v1.0 fixture check-unsupported-v11/in/reflection.vhd, now a v1.1
-- simulation: use std.reflection and mirror an integer.
use std.reflection.all;

entity reflection is
end entity reflection;

architecture sim of reflection is
begin
  process
    variable x : integer := 42;
    variable vm : value_mirror;
  begin
    vm := x'reflect;
    report "class = " & value_class'image(vm.get_value_class) &
      ", value = " & integer'image(vm.to_integer.value);
    assert vm.to_integer.value = 42 report "value" severity error;
    wait;
  end process;
end architecture sim;
