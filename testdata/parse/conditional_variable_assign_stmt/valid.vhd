architecture a of e is
begin
  process is
  begin
    v := 1 when c else 2 when d else 3;
  end process;
end;
