architecture a of e is
begin
  g : case n generate
    when a0 : 0 | 1 =>
      s <= a;
    when others =>
      s <= b;
  end generate;
end;
