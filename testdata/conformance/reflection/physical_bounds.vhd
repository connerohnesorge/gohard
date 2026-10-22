-- The bounds of a physical subtype are PHYSICAL_VALUE_MIRRORs.
use std.reflection.all;

entity physical_bounds_tb is
end entity physical_bounds_tb;

architecture sim of physical_bounds_tb is
  type dist is range 0 to 1000
    units
      mm;
      cm = 10 mm;
    end units;
  subtype short is dist range 5 mm to 50 mm;
begin
  check : process
    variable psm : physical_subtype_mirror;
  begin
    psm := short'reflect.to_physical;
    report psm.simple_name & ": left " & psm.left.image & ", right " &
      psm.right.image & ", low " & psm.low.image & ", high " &
      psm.high.image & ", length " & index'image(psm.length);
    wait;
  end process check;
end architecture sim;
