entity env_version is
end entity env_version;

architecture sim of env_version is
begin
  process
  begin
    report std.env.tool_name & " " & std.env.vhdl_version;
    wait;
  end process;
end architecture sim;
