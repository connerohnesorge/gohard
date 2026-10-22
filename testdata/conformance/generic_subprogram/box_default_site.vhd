-- An is <> default of a generic subprogram denotes the subprogram of
-- that designator visible where the instance is made (LRM 6.5.6.2):
-- here gt is declared only in the architecture that calls mx with a
-- generic map, not where mx is declared, and reverses the comparison.
-- The VHDL-2008 style instance declared in the same architecture sees
-- the same gt.
package box_pkg is
  function mx
    generic (type t; function gt (a, b : t) return boolean is <>)
    parameter (a, b : t) return t;
end package box_pkg;

package body box_pkg is
  function mx
    generic (type t; function gt (a, b : t) return boolean is <>)
    parameter (a, b : t) return t is
  begin
    if gt(a, b) then
      return a;
    end if;
    return b;
  end function mx;
end package body box_pkg;

use work.box_pkg.all;

entity box_default_site_tb is
end entity box_default_site_tb;

architecture sim of box_default_site_tb is
  function gt (x, y : integer) return boolean is
  begin
    return x < y;
  end function gt;

  function mi is new mx generic map (t => integer);
begin
  check : process
    variable v : integer;
  begin
    v := mx generic map (t => integer) (1, 2);
    report "call = " & integer'image(v);
    assert v = 1 severity failure;
    v := mi(1, 2);
    report "instance = " & integer'image(v);
    assert v = 1 severity failure;
    wait;
  end process check;
end architecture sim;
