architecture a of e is
begin
  process is
  begin
    with n select v := 1 when 0, 2 when, 3 when others;
  end process;
end;
