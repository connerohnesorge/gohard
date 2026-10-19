architecture a of e is
begin
  process is
  begin
    lbl : if a then
      null;
    else
      x := 0;
    end if lbl;
  end process;
end;
