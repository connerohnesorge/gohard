-- gohard-ci-stub: CI-only stand-in written for GoHard (#1), not vendor
-- text. It declares only what docs/libraries.md uses; map your own
-- Vivado install's data/vhdl/src/unimacro folder instead.
library ieee;
use ieee.std_logic_1164.all;

package vcomponents is
  component COUNTER_TC_MACRO is
    generic (
      COUNT_BY      : std_logic_vector := x"000000000001";
      DEVICE        : string           := "7SERIES";
      DIRECTION     : string           := "UP";
      RESET_UPON_TC : string           := "FALSE";
      TC_VALUE      : std_logic_vector := x"000000000000";
      WIDTH_DATA    : integer          := 48
    );
    port (
      Q   : out std_logic_vector(WIDTH_DATA - 1 downto 0);
      TC  : out std_logic;
      CE  : in  std_logic;
      CLK : in  std_logic;
      RST : in  std_logic
    );
  end component COUNTER_TC_MACRO;
end package vcomponents;
