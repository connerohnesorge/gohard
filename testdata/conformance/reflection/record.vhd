-- RECORD_VALUE_MIRROR and RECORD_SUBTYPE_MIRROR: elements counted from
-- 0, by index and by name.
use std.reflection.all;

entity record_tb is
end entity record_tb;

architecture sim of record_tb is
  type rec is record
    a : integer;
    b : bit;
    c : real;
  end record rec;
begin
  check : process
    variable rc  : rec := (7, '1', 1.5);
    variable rvm : record_value_mirror;
    variable rsm : record_subtype_mirror;
  begin
    rvm := rc'reflect.to_record;
    rsm := rvm.get_subtype_mirror;
    report rsm.simple_name & ": length " & index'image(rsm.length) &
      ", element 1 " & rsm.element_name(1) & ", c at " &
      index'image(rsm.element_index("C")) & ", subtype 0 " &
      rsm.element_subtype(0).simple_name & ", subtype b " &
      rsm.element_subtype("b").simple_name;
    report "elements " & rvm.get(0).to_integer.image & ", " &
      rvm.get("b").to_enumeration.image & ", " &
      rvm.get(2).to_floating.image;
    wait;
  end process check;
end architecture sim;
