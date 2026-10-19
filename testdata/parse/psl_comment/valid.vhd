-- test: std=2008 psl.comments kind=PSLAssert
architecture a of e is
begin
  -- psl default clock is rising_edge(clk);
  -- psl assert always (req -> next ack);
  -- psl assert always (a ->
  --   next b);
end;
