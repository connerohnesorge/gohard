architecture a of e is
begin
  u1 : comp port map (a => s);
  u2 : component comp generic map (n => 1) port map (s);
  u3 : entity work.f(rtl) port map (a => s);
  u4 : configuration work.cfg;
end;
