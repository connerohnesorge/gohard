architecture a of e is
begin
  assert s = '1' report "s low" severity warning;
  c1 : postponed assert t = '0';
end;
