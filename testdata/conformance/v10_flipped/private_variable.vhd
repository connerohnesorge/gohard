-- The v1.0 fixture check-unsupported-v11/in/private_variable.vhd, now
-- a v1.1 simulation: a private variable used by the methods.
package private_variable is
  type t is protected
    private variable n : integer := 0;
    procedure inc;
    impure function get return integer;
  end protected t;
end package private_variable;

package body private_variable is
  type t is protected body
    procedure inc is
    begin
      n := n + 1;
    end procedure inc;
    impure function get return integer is
    begin
      return n;
    end function get;
  end protected body t;
end package body private_variable;

use work.private_variable.all;

entity private_variable_tb is
end entity private_variable_tb;

architecture sim of private_variable_tb is
  shared variable c : t;
begin
  process
  begin
    c.inc;
    c.inc;
    report "n = " & integer'image(c.get);
    assert c.get = 2 report "n" severity error;
    wait;
  end process;
end architecture sim;
