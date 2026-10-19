architecture a of e is
begin
  s1 <= a;
  p : process begin wait; end process;
  u : entity work.f;
end;
