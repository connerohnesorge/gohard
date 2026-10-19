architecture a of e is
begin
  assert always (a -> next_a[1 to 3] (b));
  assert always (a -> next_a![2 to 4] (b));
end;
