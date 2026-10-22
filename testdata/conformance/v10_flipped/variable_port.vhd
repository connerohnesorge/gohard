-- The v1.0 fixture check-unsupported-v11/in/variable_port.vhd, now a
-- v1.1 simulation: a variable port bound to a shared variable.
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

entity variable_port is
  port (variable c : inout counter_t);
end entity variable_port;

architecture rtl of variable_port is
begin
  process
  begin
    c.inc;
    wait;
  end process;
end architecture rtl;

use work.counter.all;

entity variable_port_tb is
end entity variable_port_tb;

architecture sim of variable_port_tb is
  shared variable sv : counter_t;
begin
  u : entity work.variable_port port map (c => sv);

  process
  begin
    wait for 1 ns;
    report "count = " & integer'image(sv.get);
    assert sv.get = 1 report "count" severity error;
    wait;
  end process;
end architecture sim;
