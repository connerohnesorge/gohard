-- textio_env: design under test for the textio_env corpus design (#8).
-- An 8-bit inverter; the testbench logs it through textio.
library ieee;
use ieee.std_logic_1164.all;

entity textio_env is
  port (
    a : in  std_logic_vector(7 downto 0);
    y : out std_logic_vector(7 downto 0)
  );
end entity textio_env;

architecture rtl of textio_env is
begin
  y <= not a;
end architecture rtl;
