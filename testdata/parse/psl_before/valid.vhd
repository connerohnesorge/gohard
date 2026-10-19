architecture a of e is
begin
  assert always (a -> b before c);
  assert always (a -> b before!_ c);
end;
