-- The v1.0 fixture check-unsupported-v11/in/method_alias.vhd, now a
-- v1.1 simulation: an alias in a protected type declaration of a
-- method of a private variable calls that method on the private
-- variable of the object.
package method_alias is
  type counter is protected
    procedure inc;
    impure function get return integer;
  end protected counter;

  type t is protected
    private variable c : counter;
    alias bump is c.inc [];
    alias count is c.get [return integer];
  end protected t;
end package method_alias;

package body method_alias is
  type counter is protected body
    variable n : integer := 0;
    procedure inc is
    begin
      n := n + 1;
    end procedure inc;
    impure function get return integer is
    begin
      return n;
    end function get;
  end protected body counter;

  type t is protected body
  end protected body t;
end package body method_alias;

use work.method_alias.all;

entity method_alias_tb is
end entity method_alias_tb;

architecture sim of method_alias_tb is
  shared variable a, b : t;
begin
  process
  begin
    a.bump;
    a.bump;
    b.bump;
    report "a.count = " & integer'image(a.count) & ", b.count = " &
      integer'image(b.count);
    assert a.count = 2 and b.count = 1 report "method alias"
      severity error;
    wait;
  end process;
end architecture sim;
