-- Generic actuals and defaults and signal initial values computed
-- from std.env natives during elaboration (LRM 14.4.2, 16.5).
use std.env.all;

entity elab_values_sub is
  generic (
    version : string;
    tool    : string   := TOOL_NAME;
    line    : positive := FILE_LINE
  );
end entity elab_values_sub;

architecture sim of elab_values_sub is
  signal len : natural := tool'length;
begin
  process
  begin
    assert len > 0 report "empty TOOL_NAME" severity error;
    report "sub " & version & " " & integer'image(line);
    wait;
  end process;
end architecture sim;

use std.env.all;

entity elab_values_tb is
end entity elab_values_tb;

architecture sim of elab_values_tb is
  constant ARCH_LINE : positive := FILE_LINE;
  signal fixed   : string(1 to 4) := VHDL_VERSION;
  signal open_s  : string := VHDL_VERSION & "!";
  signal line_s  : integer := ARCH_LINE;
  signal here_s  : positive := FILE_LINE;
  signal unset_s : string(1 to 2) :=
    GETENV("GOHARD_CONFORMANCE_UNSET") & "[]";
  signal secs    : real := TIME_TO_SECONDS(2 ns);
begin
  u1 : entity work.elab_values_sub
    generic map (version => std.env.vhdl_version);
  u2 : entity work.elab_values_sub
    generic map (version => "v" & VHDL_VERSION, line => 3);

  process
  begin
    report fixed & " " & open_s & " " & integer'image(open_s'length);
    report integer'image(line_s) & " " & integer'image(here_s) & " " &
      unset_s;
    assert secs = 2.0e-9 report "TIME_TO_SECONDS" severity error;
    wait;
  end process;
end architecture sim;
