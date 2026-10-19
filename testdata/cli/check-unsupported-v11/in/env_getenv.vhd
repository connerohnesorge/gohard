entity env_getenv is
end entity env_getenv;

architecture sim of env_getenv is
begin
  process
  begin
    report std.env.getenv("HOME");
    wait;
  end process;
end architecture sim;
