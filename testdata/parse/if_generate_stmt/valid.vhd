architecture a of e is
begin
  g : if a1 : n = 1 generate
    s <= a;
  elsif a2 : n = 2 generate
    s <= b;
  else a3 : generate
    s <= c;
  end generate g;
end;
