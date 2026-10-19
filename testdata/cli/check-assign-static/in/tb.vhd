entity tb is end;
architecture a of tb is
begin
  u : entity work.leaf generic map (W => 2);
  v : entity work.leaf generic map (W => 4);
end;
