architecture a of e is
begin
  g : for i in 0 to 1 generate
    signal t : bit;
  begin
    t <= a(i);
  end;
  end generate;
end;
