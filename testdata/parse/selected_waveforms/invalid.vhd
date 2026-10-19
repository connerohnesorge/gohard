architecture a of e is
begin
  with sel select s <= a after 1 ns when "00", when others;
end;
