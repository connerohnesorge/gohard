-- nvc-differs: NVC reads the real environment; GoHard's is empty
-- unless --env or --real-env sets it
-- The v1.0 fixture check-unsupported-v11/in/env_getenv.vhd, now a v1.1
-- simulation: GETENV of an unset variable is "".
entity env_getenv is
end entity env_getenv;

architecture sim of env_getenv is
begin
  process
  begin
    report "HOME = """ & std.env.getenv("HOME") & """";
    assert std.env.getenv("HOME") = "" report "getenv" severity error;
    wait;
  end process;
end architecture sim;
