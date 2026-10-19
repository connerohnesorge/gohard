architecture a of e is
begin
  process is
  begin
    if a then
      x := 1;
    elsif b then
      x := 2;
    else
      x := 3;
    end if;
  end process;
end;
