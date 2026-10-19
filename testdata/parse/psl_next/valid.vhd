architecture a of e is
begin
  assert always (a -> next b);
  assert always (a -> next![3] b);
end;
