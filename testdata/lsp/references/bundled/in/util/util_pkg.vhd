library ieee;
use ieee.std_logic_1164.all;

-- Shared definitions of the navigation fixtures.
package util_pkg is
  -- The controller states.
  type state_t is (idle, busy, done);

  -- A bus transfer.
  type xfer_t is record
    addr : natural;
    -- The data byte.
    data : std_logic_vector(7 downto 0);
  end record xfer_t;

  -- The bus width in bits.
  constant WIDTH : natural := 8;
  alias BUS_W is WIDTH;

  -- Increments x by one.
  function inc (x : integer) return integer;
end package util_pkg;

package body util_pkg is
  function inc (x : integer) return integer is
  begin
    return x + 1;
  end function inc;
end package body util_pkg;
