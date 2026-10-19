architecture a of e is
begin
  process is
  begin
    s <= a when c = '1' else b when d = '1' else '0';
  end process;
end;
