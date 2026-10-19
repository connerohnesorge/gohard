architecture a of e is
begin
  with sel select s <= a when '0', b when others;
  with sel select? t <= c when "1-", d when others;
end;
