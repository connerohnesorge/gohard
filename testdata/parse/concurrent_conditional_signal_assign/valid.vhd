architecture a of e is
begin
  s <= a when sel = '1' else b;
  t <= guarded c when en = '1' else unaffected;
end;
