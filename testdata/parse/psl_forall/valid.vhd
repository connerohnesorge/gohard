architecture a of e is
begin
  assert forall i in {0 to 3} : always (req(i) -> next ack(i));
  assert forall k in boolean : always (k -> a);
end;
