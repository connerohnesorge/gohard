-- The v1.0 fixture check-unsupported-v11/in/env_dir.vhd, now a v1.1
-- simulation: DIR_WORKINGDIR names the working directory, the project
-- root (".") when the run starts.
-- nvc-differs: NVC names the working directory by its absolute path; GoHard names it relative to the project root (#18)
entity env_dir is
end entity env_dir;

architecture sim of env_dir is
begin
  process
  begin
    report std.env.dir_workingdir;
    assert std.env.dir_itemisdir(std.env.dir_workingdir)
      report "working directory" severity error;
    wait;
  end process;
end architecture sim;
