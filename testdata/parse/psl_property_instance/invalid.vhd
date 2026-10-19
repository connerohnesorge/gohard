architecture a of e is
  property handshake (boolean r, q) is always (r -> next q);
begin
  assert handshake(req, );
end;
