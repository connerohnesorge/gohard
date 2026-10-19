architecture a of e is
begin
  process is
  begin
    s <= '0', '1' after 5 ns, '0' after 10 ns;
    t <= unaffected;
  end process;
end;
