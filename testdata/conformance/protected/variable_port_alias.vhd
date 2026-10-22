-- The actual of a variable port may be an alias of a shared variable:
-- the alias denotes the shared variable itself (LRM 6.5.7.3, 6.6.2).
package variable_port_alias_pkg is
  type counter_t is protected
    procedure add(k : integer);
    impure function get return integer;
  end protected counter_t;
end package variable_port_alias_pkg;

package body variable_port_alias_pkg is
  type counter_t is protected body
    variable n : integer := 0;
    procedure add(k : integer) is
    begin
      n := n + k;
    end procedure add;
    impure function get return integer is
    begin
      return n;
    end function get;
  end protected body counter_t;
end package body variable_port_alias_pkg;

use work.variable_port_alias_pkg.all;

entity variable_port_alias_sub is
  generic (k : integer);
  port (variable c : inout counter_t);
end entity variable_port_alias_sub;

architecture sim of variable_port_alias_sub is
begin
  process
  begin
    c.add(k);
    wait;
  end process;
end architecture sim;

use work.variable_port_alias_pkg.all;

entity variable_port_alias_tb is
end entity variable_port_alias_tb;

architecture sim of variable_port_alias_tb is
  shared variable one   : counter_t;
  shared variable other : counter_t;
  alias al is one;
  alias al2 is al;
begin
  u1 : entity work.variable_port_alias_sub
    generic map (k => 1) port map (c => al);
  u2 : entity work.variable_port_alias_sub
    generic map (k => 10) port map (c => one);
  g : for i in 1 to 2 generate
    u : entity work.variable_port_alias_sub
      generic map (k => 100 * i) port map (c => al2);
  end generate g;
  u3 : entity work.variable_port_alias_sub
    generic map (k => 5) port map (c => other);

  process
  begin
    wait for 1 ns;
    report "one " & integer'image(one.get) & " other " &
      integer'image(other.get);
    wait;
  end process;
end architecture sim;
