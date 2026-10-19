architecture a of e is
begin
  process is
  begin
    wait on a, b until c = '1' for 10 ns;
    wait;
  end process;
end;
