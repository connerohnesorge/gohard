-- Shared-variable ports of protected type (VHDL-2019, LRM 6.5.2): a
-- port of class variable is bound to the shared variable of its
-- actual, so every method call through the port reaches that object.
-- Two instances bound to two shared variables stay separate, and a
-- variable port can be passed on to a lower instance.
package counter_pkg is
  type counter_t is protected
    procedure inc;
    impure function get return integer;
  end protected counter_t;
end package counter_pkg;

package body counter_pkg is
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
end package body counter_pkg;

use work.counter_pkg.all;

entity dut is
  generic (times : positive);
  port (variable c : inout counter_t);
end entity dut;

architecture rtl of dut is
begin
  process
  begin
    for i in 1 to times loop
      c.inc;
    end loop;
    wait;
  end process;
end architecture rtl;

use work.counter_pkg.all;

entity mid is
  port (variable c : inout counter_t);
end entity mid;

architecture rtl of mid is
begin
  u : entity work.dut
    generic map (times => 2)
    port map (c => c);
end architecture rtl;

use work.counter_pkg.all;

entity vport_tb is
end entity vport_tb;

architecture sim of vport_tb is
  shared variable a, b : counter_t;
begin
  da : entity work.dut
    generic map (times => 3)
    port map (c => a);

  db : entity work.mid
    port map (c => b);

  check : process
  begin
    wait for 1 ns;
    report "a = " & integer'image(a.get) & ", b = " &
      integer'image(b.get);
    assert a.get = 3 and b.get = 2 report "wrong counts" severity error;
    wait;
  end process check;
end architecture sim;
