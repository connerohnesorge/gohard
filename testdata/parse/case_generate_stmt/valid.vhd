architecture a of e is
begin
  g : case n generate
    when 0 => s <= a;
    when others => s <= b;
  end generate g;
end;
