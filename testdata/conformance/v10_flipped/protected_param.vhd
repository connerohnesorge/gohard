-- The v1.0 fixture check-unsupported-v11/in/protected_param.vhd, now a
-- v1.1 simulation: a function with a protected-type parameter.
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

package protected_param is
  impure function peek (variable c : inout counter_t) return integer;
end package protected_param;

package body protected_param is
  impure function peek (variable c : inout counter_t) return integer is
  begin
    c.inc;
    return c.get;
  end function peek;
end package body protected_param;

use work.counter.all;
use work.protected_param.all;

entity protected_param_tb is
end entity protected_param_tb;

architecture sim of protected_param_tb is
  shared variable c : counter_t;
begin
  process
    variable a, b : integer;
  begin
    a := peek(c);
    b := peek(c);
    report "peek = " & integer'image(a) & " " & integer'image(b);
    assert a = 1 and b = 2 and c.get = 2 report "peek" severity error;
    wait;
  end process;
end architecture sim;
