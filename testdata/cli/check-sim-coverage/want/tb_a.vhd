-- tb_a: clamping (mode 1) and the resolved signals of util.
use work.util.all;

entity tb_a is
end entity tb_a;

architecture sim of tb_a is
  signal mode, x, y : integer := 0;
  signal flag       : bit;
  signal w          : wired := '0';
  signal wv         : wired_vector(3 downto 0);
begin
  u : entity work.dut
    port map (mode => mode, x => x, y => y, flag => flag);

  w  <= '1' after 5 ns;
  wv <= "1010";

  stim : process
  begin
    w    <= '0';
    mode <= 1;
    x    <= 20;
    wait for 10 ns;
    assert y = 10 report "clamp high" severity error;
    x <= -5;
    wait for 10 ns;
    assert y = 0 and flag = '1' report "clamp low" severity error;
    assert w = '1' and wv = "1010" report "wired" severity error;
    wait;
  end process stim;
end architecture sim;
