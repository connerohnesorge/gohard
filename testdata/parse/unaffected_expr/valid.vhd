architecture a of e is
begin
  s <= unaffected when en = '0' else d;
  process is
  begin
    t <= unaffected;
    wait;
  end process;
end;
