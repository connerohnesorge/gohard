-- PHYSICAL_VALUE_MIRROR and PHYSICAL_SUBTYPE_MIRROR: values in primary
-- units, units counted from 1, scales.
use std.reflection.all;

entity physical_tb is
end entity physical_tb;

architecture sim of physical_tb is
  type dist is range 0 to 1000
    units
      mm;
      cm = 10 mm;
      m = 100 cm;
    end units;
begin
  check : process
    variable d   : dist := 25 cm;
    variable t   : time := 3 ns;
    variable pvm : physical_value_mirror;
    variable psm : physical_subtype_mirror;
  begin
    pvm := d'reflect.to_physical;
    psm := pvm.get_subtype_mirror;
    report "value " & integer'image(pvm.value) & ", unit " &
      index'image(pvm.unit_index) & ", image " & pvm.image;
    report psm.simple_name & ": units " & index'image(psm.units_length) &
      ", first " & psm.unit_name(1) & ", last " & psm.unit_name(3) &
      ", m at " & index'image(psm.unit_index("M")) & ", scale 3 " &
      integer'image(psm.scale(3)) & ", scale cm " &
      integer'image(psm.scale("cm")) & ", length " &
      index'image(psm.length) & ", ascending " &
      boolean'image(psm.ascending);
    report "time " & integer'image(t'reflect.to_physical.value) & ", " &
      t'reflect.to_physical.image & ", " &
      t'reflect.get_subtype_mirror.simple_name & ", units " &
      index'image(time'reflect.to_physical.units_length) & ", unit 6 " &
      time'reflect.to_physical.unit_name(6);
    wait;
  end process check;
end architecture sim;
