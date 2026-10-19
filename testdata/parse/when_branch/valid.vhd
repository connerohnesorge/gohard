architecture a of e is
begin
  process is
  begin
    v := a when p else b when q else c;
  end process;
end;
