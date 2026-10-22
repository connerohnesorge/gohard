-- nvc-differs: TOOL_NAME is the tool's own name
-- The v1.0 fixture check-unsupported-v11/in/env_version.vhd, now a
-- v1.1 simulation: TOOL_NAME and VHDL_VERSION.
entity env_version is
end entity env_version;

architecture sim of env_version is
begin
  process
  begin
    report std.env.tool_name & " " & std.env.vhdl_version;
    assert std.env.vhdl_version = "2019" report "version" severity error;
    wait;
  end process;
end architecture sim;
