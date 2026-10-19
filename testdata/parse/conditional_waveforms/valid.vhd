architecture a of e is
begin
  s <= a after 1 ns when c = '1' else b, d after 2 ns when e = '1' else '0';
end;
