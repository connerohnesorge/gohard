architecture a of e is
begin
  a1 : assert always (req -> next ack) report "no ack" severity error;
end;
