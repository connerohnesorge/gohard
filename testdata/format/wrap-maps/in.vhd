entity top is
end entity;
architecture rtl of top is
begin
  u_fifo : entity work.fifo generic map (WIDTH => 16, DEPTH => 512, ALMOST_FULL_LEVEL => 500) port map (clk => clk, rst => rst, wr_en => push, din => data, full => is_full); -- the fifo
  u_short : entity work.leaf port map (a => b);
end architecture;
