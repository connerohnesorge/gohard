-- nvc-differs: NVC 1.23 rejects arrays of protected types
-- The v1.0 fixture check-unsupported-v11/in/protected_array.vhd, now a
-- v1.1 simulation: an unconstrained array of a protected type.
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

package protected_array is
  type counters_t is array (natural range <>) of counter_t;
end package protected_array;

use work.counter.all;
use work.protected_array.all;

entity protected_array_tb is
end entity protected_array_tb;

architecture sim of protected_array_tb is
begin
  process
    variable cs : counters_t(0 to 2);
  begin
    cs(1).inc;
    cs(2).inc;
    cs(2).inc;
    report "counts = " & integer'image(cs(0).get) & " " &
      integer'image(cs(1).get) & " " & integer'image(cs(2).get);
    assert cs(2).get = 2 report "count" severity error;
    wait;
  end process;
end architecture sim;
