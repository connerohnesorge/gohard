-- 'SIMPLE_NAME, 'PATH_NAME and 'INSTANCE_NAME (LRM 16.2.5) across a
-- hierarchy: entity and component instances, for generate iterations,
-- an if generate alternative label, a block, labeled and unlabeled
-- processes, a loop, a procedure of a process and a function of a
-- package. Each string is checked against NVC 1.23's form.
package names_pkg is
  constant c : integer := 1;
  function f(x : integer) return string;
  procedure check(tag, got, want : string);
end package names_pkg;

package body names_pkg is
  function f(x : integer) return string is
    variable v : integer;
  begin
    return f'path_name & " " & v'instance_name & " " & c'path_name;
  end function f;

  procedure check(tag, got, want : string) is
  begin
    report tag & " " & got;
    assert got = want report "wrong " & tag & ": " & got severity error;
  end procedure check;
end package body names_pkg;

use work.names_pkg.all;

entity names_sub is
  generic (g : integer := 0);
  port (p : in bit);
end entity names_sub;

architecture a of names_sub is
  signal s : bit;
begin
  pr : process
    variable v : integer;

    procedure q(y : integer) is
      variable w : integer;
    begin
      report "q " & q'path_name & " " & w'instance_name & " " &
        y'path_name;
    end procedure q;
  begin
    report "s " & s'path_name & " " & s'instance_name & " " &
      s'simple_name;
    report "pg " & p'path_name & " " & g'instance_name;
    report "v " & v'path_name & " " & v'instance_name;
    report "pr " & pr'path_name & " " & pr'instance_name & " " &
      pr'simple_name;
    report "sub " & names_sub'path_name & " " & a'instance_name;
    q(1);
    wait;
  end process pr;
end architecture a;

use work.names_pkg.all;

entity names_tb is
end entity names_tb;

architecture rtl of names_tb is
  signal t : bit;

  component names_sub is
    generic (g : integer := 0);
    port (p : in bit);
  end component names_sub;
begin
  u1 : entity work.names_sub generic map (1) port map (t);

  gen : for i in 0 to 1 generate
    signal gs : bit;
  begin
    b : block
    begin
      process
      begin
        report "gen " & gs'path_name & " " & i'instance_name & " " &
          b'path_name;
        wait;
      end process;
    end block b;
  end generate gen;

  ig : if true generate
    u3 : names_sub port map (t);
  end generate ig;

  ig2 : if alt1 : false generate
  elsif alt2 : true generate
    signal as : bit;
  begin
    pp : process
    begin
      check("alt", as'path_name & " " & as'instance_name & " " &
        pp'path_name, ":names_tb:alt2:as :names_tb(rtl):alt2:as " &
        ":names_tb:alt2:pp:");
      wait;
    end process pp;
  end generate ig2;

  process
    variable u : integer;
  begin
    check("top", u'path_name & " " & names_tb'path_name & " " &
      rtl'instance_name & " " & t'instance_name,
      ":names_tb::u :names_tb: :names_tb(rtl): :names_tb(rtl):t");
    check("f", f(1), ":work:names_pkg:f[integer return string]: " &
      ":work:names_pkg:f[integer return string]:v :work:names_pkg:c");
    check("pkg", work.names_pkg'path_name, ":work:names_pkg:");
    for j in 1 to 1 loop
      check("loop", j'path_name, ":names_tb::j");
    end loop;
    check("sub", t'simple_name, "t");
    wait;
  end process;
end architecture rtl;
