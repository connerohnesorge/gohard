-- The v1.0 fixture check-unsupported-v11/in/image_composite.vhd, now a
-- v1.1 simulation: 'IMAGE of an array type in a package constant.
package image_composite is
  constant v : bit_vector(0 to 3) := "1010";
  constant s : string := bit_vector'image(v);
end package image_composite;

use work.image_composite.all;

entity image_composite_tb is
end entity image_composite_tb;

architecture sim of image_composite_tb is
begin
  process
  begin
    report s;
    assert s = """1010""" report "image" severity error;
    wait;
  end process;
end architecture sim;
