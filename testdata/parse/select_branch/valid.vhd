architecture a of e is
begin
  process is
  begin
    with n select s <= a when 0, b when others;
  end process;
end;
