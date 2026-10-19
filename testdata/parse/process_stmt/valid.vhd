architecture a of e is
begin
  p1 : postponed process (clk) is
    variable v : integer;
  begin
    v := 0;
  end postponed process p1;
end;
