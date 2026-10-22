-- GETENV sees only simulation.env.vars and --env: a name that is not
-- set gives "" (STRING) and null (LINE) (#18).
use std.env.all;
use std.textio.all;

entity getenv_tb is
end entity getenv_tb;

architecture sim of getenv_tb is
begin
  check : process
    variable l : line;
  begin
    report "GETENV = """ & GETENV("GOHARD_CONFORMANCE_UNSET") & """";
    l := GETENV("GOHARD_CONFORMANCE_UNSET");
    report "GETENV line is null: " & boolean'image(l = null);
    assert GETENV("GOHARD_CONFORMANCE_UNSET") = ""
      report "unset name" severity failure;
    wait;
  end process check;
end architecture sim;
