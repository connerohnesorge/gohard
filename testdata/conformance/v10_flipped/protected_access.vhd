-- The v1.0 fixture check-unsupported-v11/in/protected_access.vhd, now
-- a v1.1 simulation: an access type to a protected type.
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

package protected_access is
  type counter_ptr is access counter_t;
end package protected_access;

use work.counter.all;
use work.protected_access.all;

entity protected_access_tb is
end entity protected_access_tb;

architecture sim of protected_access_tb is
begin
  process
    variable p : counter_ptr;
  begin
    p := new counter_t;
    p.inc;
    p.inc;
    report "count = " & integer'image(p.get);
    assert p.get = 2 report "count" severity error;
    deallocate(p);
    wait;
  end process;
end architecture sim;
