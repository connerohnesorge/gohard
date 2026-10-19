architecture a of e is
begin
  assert (always (a -> next b)) abort rst;
  assert (always a) async_abort rst;
  assert (always a) sync_abort rst;
end;
