architecture a of e is
begin
  assert always (a -> next_event(c)(b));
  assert always (a -> next_event!(c)[2](b));
end;
