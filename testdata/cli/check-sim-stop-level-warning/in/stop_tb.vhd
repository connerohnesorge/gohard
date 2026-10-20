entity stop_tb is
end entity;

architecture sim of stop_tb is
begin
  process
  begin
    wait for 1 ns;
    assert false report "first" severity warning;
    wait for 1 ns;
    assert false report "second" severity error;
    wait for 1 ns;
    report "third";
    wait;
  end process;
end architecture;
