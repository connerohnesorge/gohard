-- force_out: an inverter whose out port y the testbench forces with
-- force mode out (#63). y_seen reads y back (VHDL-2008 lets a design
-- read its out ports), so the port's own value is observable.
library ieee;
use ieee.std_logic_1164.all;

entity force_out is
  port (
    a      : in  std_logic;
    y      : out std_logic;
    y_seen : out std_logic
  );
end entity force_out;

architecture rtl of force_out is
begin
  y      <= not a;
  y_seen <= y;
end architecture rtl;
