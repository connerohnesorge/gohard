-- nvc-differs: NVC 1.23 indexes descending arrays from the left bound
-- GET of an array value mirror takes index values, whatever the
-- direction or the left bound of the range.
use std.reflection.all;

entity array_descending_tb is
end entity array_descending_tb;

architecture sim of array_descending_tb is
  type arr is array (integer range <>) of integer;
begin
  check : process
    variable bv : bit_vector(3 downto 0) := "1000";
    variable a  : arr(10 to 12) := (1, 2, 3);
  begin
    report "bv(3) = " & bv'reflect.to_array.get(3).to_enumeration.image &
      ", bv(0) = " & bv'reflect.to_array.get(0).to_enumeration.image;
    report "a(11) = " & a'reflect.to_array.get(11).to_integer.image;
    wait;
  end process check;
end architecture sim;
