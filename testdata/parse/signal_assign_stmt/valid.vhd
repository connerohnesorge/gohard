architecture a of e is
begin
  process is
  begin
    s <= '1';
    t <= transport a after 1 ns, b after 2 ns;
  end process;
end;
