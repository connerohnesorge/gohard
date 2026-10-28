-- tb_b: pass-through (mode 0) and negation (mode 2).
entity tb_b is
end entity tb_b;

architecture sim of tb_b is
  signal mode, x, y : integer := 0;
  signal flag       : bit;
begin
  u : entity work.dut
    port map (mode => mode, x => x, y => y, flag => flag);

  stim : process
  begin
    mode <= 0;
    x    <= 7;
    wait for 10 ns;
    assert y = 7 report "pass" severity error;
    mode <= 2;
    x    <= 200;
    wait for 10 ns;
    assert y = -200 and flag = '1' report "negate" severity error;
    wait;
  end process stim;
end architecture sim;
