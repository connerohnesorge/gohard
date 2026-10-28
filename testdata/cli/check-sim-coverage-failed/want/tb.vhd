-- tb: an assertion error stops the run at 10 ns; the coverage of the
-- run up to there is still written (#62).
entity tb is
end entity tb;

architecture sim of tb is
  signal n : integer := 0;
begin
  count : process
  begin
    for i in 1 to 3 loop
      n <= i;
      wait for 10 ns;
      assert n /= 1 report "stop at one" severity error;
    end loop;
    wait;
  end process count;
end architecture sim;
