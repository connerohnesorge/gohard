architecture a of e is
begin
  assert always (a -> b until c);
  assert always (a -> b until!_ c);
  assert always (a -> b until_ c);
end;
