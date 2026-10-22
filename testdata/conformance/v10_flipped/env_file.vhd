-- The v1.0 fixture check-unsupported-v11/in/env_file.vhd, now a v1.1
-- simulation: FILE_NAME and FILE_LINE of the calling line.
entity env_file is
end entity env_file;

architecture sim of env_file is
begin
  process
  begin
    report std.env.file_name & ":" & integer'image(std.env.file_line);
    assert std.env.file_line = 11 report "line" severity error;
    wait;
  end process;
end architecture sim;
