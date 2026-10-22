-- nvc-differs: NVC 1.23 rejects records of protected types
-- The v1.0 fixture check-unsupported-v11/in/protected_record.vhd, now
-- a v1.1 simulation: a record of two protected objects.
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

package protected_record is
  type pair_t is record
    a, b : counter_t;
  end record pair_t;
end package protected_record;

use work.counter.all;
use work.protected_record.all;

entity protected_record_tb is
end entity protected_record_tb;

architecture sim of protected_record_tb is
begin
  process
    variable p : pair_t;
  begin
    p.a.inc;
    p.b.inc;
    p.b.inc;
    report "pair = " & integer'image(p.a.get) & " " &
      integer'image(p.b.get);
    assert p.a.get = 1 and p.b.get = 2 report "pair" severity error;
    wait;
  end process;
end architecture sim;
