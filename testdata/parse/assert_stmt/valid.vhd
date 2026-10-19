architecture a of e is
begin
  process is
  begin
    assert x = 1 report "x is " & integer'image(x) severity error;
    assert y;
  end process;
end;
