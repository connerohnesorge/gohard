architecture a of e is
begin
  with sel select s <= a after 1 ns when "00", b, c after 2 ns when others;
end;
