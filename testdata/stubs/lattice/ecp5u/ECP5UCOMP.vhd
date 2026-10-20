-- gohard-ci-stub: CI-only stand-in written for GoHard (#1), not vendor
-- text. It declares only what docs/libraries.md uses; map your own
-- Diamond install's cae_library/simulation/vhdl/ecp5u/src folder
-- instead.
library ieee;
use ieee.std_logic_1164.all;

package components is
  component OSCG is
    generic (
      DIV : integer := 128
    );
    port (
      OSC : out std_logic
    );
  end component OSCG;

  component FD1S3AX is
    generic (
      GSR : string := "ENABLED"
    );
    port (
      D  : in  std_logic;
      CK : in  std_logic;
      Q  : out std_logic
    );
  end component FD1S3AX;
end package components;
