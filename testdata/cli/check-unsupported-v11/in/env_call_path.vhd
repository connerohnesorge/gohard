entity env_call_path is
end entity env_call_path;

architecture sim of env_call_path is
begin
  process
    variable p : std.env.call_path_vector_ptr;
  begin
    p := std.env.get_call_path;
    wait;
  end process;
end architecture sim;
