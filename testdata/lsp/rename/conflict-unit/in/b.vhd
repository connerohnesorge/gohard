entity top is
end entity;
architecture a of top is
  signal x, y : bit;
begin
  u : entity work.sub port map (din => x, dout => y);
end architecture;
