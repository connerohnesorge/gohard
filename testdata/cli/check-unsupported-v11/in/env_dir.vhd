entity env_dir is
end entity env_dir;

architecture sim of env_dir is
begin
  process
  begin
    report std.env.dir_workingdir;
    wait;
  end process;
end architecture sim;
