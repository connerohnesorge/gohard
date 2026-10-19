architecture a of e is
begin
  s <= a and b;
  t <= guarded transport c after 1 ns;
end;
