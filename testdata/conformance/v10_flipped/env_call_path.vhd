-- The v1.0 fixture check-unsupported-v11/in/env_call_path.vhd, now a
-- v1.1 simulation: GET_CALL_PATH from a process.
entity env_call_path is
end entity env_call_path;

architecture sim of env_call_path is
begin
  process
    variable p : std.env.call_path_vector_ptr;
  begin
    p := std.env.get_call_path;
    report "depth " & integer'image(p'length) & ": " & p(0).name.all &
      " line " & integer'image(p(0).file_line);
    assert p'length = 1 report "depth" severity error;
    wait;
  end process;
end architecture sim;
