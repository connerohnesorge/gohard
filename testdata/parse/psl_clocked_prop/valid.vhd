architecture a of e is
begin
  assert (always a -> next b) @ rising_edge(clk);
end;
