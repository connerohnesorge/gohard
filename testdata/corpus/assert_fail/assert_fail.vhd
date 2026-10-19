-- assert_fail: design under test for the assert_fail corpus design (#8).
-- A 4-bit adder that is correct; the testbench expects a wrong sum on
-- purpose so the run fails with known messages.
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity assert_fail is
  port (
    a : in  unsigned(3 downto 0);
    b : in  unsigned(3 downto 0);
    s : out unsigned(3 downto 0)
  );
end entity assert_fail;

architecture rtl of assert_fail is
begin
  s <= a + b;
end architecture rtl;
