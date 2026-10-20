-- gohard-ci-stub: CI-only stand-in written for GoHard (#1), not vendor
-- text. It declares only what docs/libraries.md uses; map your own
-- Vivado install's data/vhdl/src/unisims folder instead.
library ieee;
use ieee.std_logic_1164.all;

package vcomponents is
  component BUFG is
    port (
      O : out std_ulogic;
      I : in  std_ulogic
    );
  end component BUFG;

  component IBUFDS is
    generic (
      DIFF_TERM  : boolean := false;
      IOSTANDARD : string  := "DEFAULT"
    );
    port (
      O  : out std_ulogic;
      I  : in  std_ulogic;
      IB : in  std_ulogic
    );
  end component IBUFDS;

  component FDRE is
    generic (
      INIT : bit := '0'
    );
    port (
      Q  : out std_ulogic;
      C  : in  std_ulogic;
      CE : in  std_ulogic;
      D  : in  std_ulogic;
      R  : in  std_ulogic
    );
  end component FDRE;
end package vcomponents;
