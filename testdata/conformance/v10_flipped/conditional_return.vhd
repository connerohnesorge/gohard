-- The v1.0 fixture check-unsupported-v11/in/conditional_return.vhd,
-- now a v1.1 simulation: return ... when in a function.
package conditional_return is
  function clip (x : integer) return integer;
end package conditional_return;

package body conditional_return is
  function clip (x : integer) return integer is
  begin
    return 0 when x < 0;
    return x;
  end function clip;
end package body conditional_return;

use work.conditional_return.all;

entity conditional_return_tb is
end entity conditional_return_tb;

architecture sim of conditional_return_tb is
begin
  process
  begin
    report "clip(-5) = " & integer'image(clip(-5)) & ", clip(7) = " &
      integer'image(clip(7));
    assert clip(-5) = 0 and clip(7) = 7 report "clip" severity error;
    wait;
  end process;
end architecture sim;
