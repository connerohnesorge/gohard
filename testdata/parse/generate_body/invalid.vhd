architecture a of e is
begin
  g : for i in 0 to 1 generate
    signal t : bit;
    t <= a(i);
  end generate;
end;
