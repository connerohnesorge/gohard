architecture a of e is
begin
  process is
  begin
    s <= a after 1 ns;
    g <= null after 2 ns;
  end process;
end;
