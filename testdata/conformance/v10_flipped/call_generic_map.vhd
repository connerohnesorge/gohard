-- The v1.0 fixture check-unsupported-v11/in/call_generic_map.vhd, now
-- a v1.1 simulation: a call with a generic map is an implicit instance
-- of the uninstantiated function.
package call_generic_map is
  function scale
    generic (k : integer)
    parameter (x : integer) return integer;
end package call_generic_map;

package body call_generic_map is
  function scale
    generic (k : integer)
    parameter (x : integer) return integer is
  begin
    return k * x;
  end function scale;

  function twice (x : integer) return integer is
  begin
    return scale generic map (k => 2) (x => x);
  end function twice;
end package body call_generic_map;

use work.call_generic_map.all;

entity call_generic_map_tb is
end entity call_generic_map_tb;

architecture sim of call_generic_map_tb is
begin
  process
    variable n : integer;
  begin
    n := scale generic map (k => 3) parameter map (x => 7);
    report "scale (k => 3) (7) = " & integer'image(n);
    assert n = 21 report "call generic map" severity error;
    wait;
  end process;
end architecture sim;
