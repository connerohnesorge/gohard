architecture a of e is
begin
  process is
  begin
    wait on a, b until for 10 ns;
  end process;
end;
