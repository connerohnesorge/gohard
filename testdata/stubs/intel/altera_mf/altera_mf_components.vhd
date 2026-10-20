-- gohard-ci-stub: CI-only stand-in written for GoHard (#1), not vendor
-- text. It declares only what docs/libraries.md uses; map your own
-- Quartus install's quartus/eda/sim_lib folder instead.
library ieee;
use ieee.std_logic_1164.all;

package altera_mf_components is
  component altsyncram is
    generic (
      operation_mode         : string  := "SINGLE_PORT";
      width_a                : natural := 8;
      widthad_a              : natural := 8;
      outdata_reg_a          : string  := "UNREGISTERED";
      intended_device_family : string  := "Cyclone V"
    );
    port (
      clock0    : in  std_logic := '1';
      address_a : in  std_logic_vector(widthad_a - 1 downto 0);
      data_a    : in  std_logic_vector(width_a - 1 downto 0) :=
        (others => '1');
      wren_a    : in  std_logic := '0';
      q_a       : out std_logic_vector(width_a - 1 downto 0)
    );
  end component altsyncram;
end package altera_mf_components;
