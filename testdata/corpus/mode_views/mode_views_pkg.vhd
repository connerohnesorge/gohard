-- mode_views: package with a record bus type, a mode view and its
-- converse alias, plus conditional-analysis directives (VHDL-2019).
library ieee;
use ieee.std_logic_1164.all;

package mode_views_pkg is

  type bus_t is record
    req  : std_logic;
    data : std_logic_vector(7 downto 0);
    ack  : std_logic;
  end record bus_t;

  -- Producer side: drives req and data, reads ack.
  view producer_v of bus_t is
    req  : out;
    data : out;
    ack  : in;
  end view producer_v;

  -- Consumer side: the converse of the producer view.
  alias consumer_v is producer_v'converse;

  -- Built-in identifier: active under 2019; the `else branch is the
  -- inactive branch (valid VHDL, so it adds no diagnostic).
`if VHDL_VERSION >= "2019" then
  constant C_STD_TAG : string := "2019";
`else
  constant C_STD_TAG : string := "old";
`end if

  -- User identifier from defines: (MV_PROFILE = "wide").
`if MV_PROFILE = "wide" then
  constant C_XOR_KEY : std_logic_vector(7 downto 0) := x"A5";
`elsif MV_PROFILE = "narrow" then
  constant C_XOR_KEY : std_logic_vector(7 downto 0) := x"0F";
`else
  `error "MV_PROFILE must be wide or narrow"
`end if

end package mode_views_pkg;
