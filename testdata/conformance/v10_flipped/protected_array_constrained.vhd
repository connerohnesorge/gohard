-- nvc-differs: NVC 1.23 rejects arrays of protected types
-- The v1.0 fixture check-unsupported-v11/in/protected_array_constrained
-- .vhd, now a v1.1 simulation: a constrained array type declares a
-- subtype of an anonymous base.
package counter is
  type counter_t is protected
    procedure inc;
    impure function get return integer;
  end protected counter_t;
end package counter;

package body counter is
  type counter_t is protected body
    variable n : integer := 0;
    procedure inc is
    begin
      n := n + 1;
    end procedure inc;
    impure function get return integer is
    begin
      return n;
    end function get;
  end protected body counter_t;
end package body counter;

use work.counter.all;

package protected_array_constrained is
  type counter_pair_t is array (0 to 1) of counter_t;
end package protected_array_constrained;

use work.counter.all;
use work.protected_array_constrained.all;

entity protected_array_constrained_tb is
end entity protected_array_constrained_tb;

architecture sim of protected_array_constrained_tb is
  shared variable pair : counter_pair_t;
begin
  process
  begin
    pair(0).inc;
    pair(1).inc;
    pair(1).inc;
    report "pair = " & integer'image(pair(0).get) & " " &
      integer'image(pair(1).get);
    assert pair(0).get = 1 and pair(1).get = 2 report "pair"
      severity error;
    wait;
  end process;
end architecture sim;
