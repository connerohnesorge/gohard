-- A floating type or subtype declared with a range has that range:
-- 'LEFT, 'RIGHT, 'ASCENDING, range checks and the subtype mirror's
-- LEFT, RIGHT, LOW, HIGH and ASCENDING (LRM 5.2.5, 16.2.2, 16.11).
use std.reflection.all;

entity floating_bounds_tb is
end entity floating_bounds_tb;

architecture sim of floating_bounds_tb is
  type fdown is range 2.5 downto -1.5;
  type fup is range -0.5 to 8.25;
  subtype unit_real is real range -1.25 to 0.75;
  subtype narrow is fup range -0.5 to 3.5;
begin
  check : process
    variable d   : fdown := 1.5;
    variable u   : fup := 0.25;
    variable ur  : unit_real := 0.5;
    variable n   : narrow := 3.25;
    variable fsm : floating_subtype_mirror;
  begin
    report "fdown " & fdown'image(fdown'left) & " " &
      fdown'image(fdown'right) & " " & fdown'image(fdown'low) & " " &
      boolean'image(fdown'ascending) & " " & fdown'image(d);
    report "unit_real " & real'image(unit_real'low) & " " &
      real'image(unit_real'high) & " " & real'image(ur);
    report "narrow " & fup'image(narrow'left) & " " &
      fup'image(narrow'right) & " " & fup'image(n);
    fsm := d'reflect.to_floating.get_subtype_mirror;
    report "mirror " & fsm.simple_name & " " & real'image(fsm.left.value) &
      " " & real'image(fsm.right.value) & " " & real'image(fsm.low.value) &
      " " & real'image(fsm.high.value) & " " & boolean'image(fsm.ascending);
    fsm := fup'reflect.to_floating;
    report "mirror " & fsm.simple_name & " " & real'image(fsm.left.value) &
      " " & real'image(fsm.high.value) & " " & boolean'image(fsm.ascending) &
      " " & u'reflect.to_floating.image;
    fsm := unit_real'reflect.to_floating;
    report "mirror " & real'image(fsm.low.value) & " " &
      real'image(fsm.high.value);
    d := d + 1.5;
    report "unreached " & fdown'image(d);
    wait;
  end process check;
end architecture sim;
