architecture a of e is
begin
  process is
  begin
    with sel select v := 1 when '0', 2 when others;
  end process;
end;
