entity env_file is
end entity env_file;

architecture sim of env_file is
begin
  process
  begin
    report std.env.file_name & ":" & integer'image(std.env.file_line);
    wait;
  end process;
end architecture sim;
