architecture a of e is
begin
  assert always (rose(a) -> next fell(b));
  assert always (stable(c) and onehot(v) and onehot0(w));
  assert always (prev(a) = b);
end;
