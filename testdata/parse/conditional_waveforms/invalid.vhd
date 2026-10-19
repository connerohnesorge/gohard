architecture a of e is
begin
  s <= a after 1 ns when c = '1' else b after;
end;
