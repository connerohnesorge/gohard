-- wait_forms: delay mechanisms.
--
-- The same input drives three delay lines: the default inertial delay
-- (pulse rejection limit equals the delay), a transport delay (every
-- pulse passes) and an inertial delay with an explicit 3 ns rejection
-- limit. The testbench exercises every form of the wait statement.
library ieee;
use ieee.std_logic_1164.all;

entity wait_forms is
  port (
    x       : in  std_logic;
    y_inert : out std_logic;
    y_trans : out std_logic;
    y_rej   : out std_logic
  );
end entity wait_forms;

architecture rtl of wait_forms is
begin
  y_inert <= x after 10 ns;
  y_trans <= transport x after 10 ns;
  y_rej   <= reject 3 ns inertial x after 10 ns;
end architecture rtl;
