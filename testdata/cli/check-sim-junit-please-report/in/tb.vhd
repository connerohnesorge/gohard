-- 'PATH_NAME of a declaration inside a generic package: the
-- interpreter does not know which instance of the package is running,
-- so it asks for a report instead of printing a possibly wrong path.
package gp is
  generic (n : integer);
  procedure show;
end package gp;

package body gp is
  procedure show is
    variable v : integer;
  begin
    report v'path_name;
  end procedure show;
end package body gp;

package gp3 is new work.gp generic map (n => 3);

entity tb is
end entity tb;

architecture sim of tb is
begin
  process
  begin
    work.gp3.show;
    report "not reached";
    wait;
  end process;
end architecture sim;
