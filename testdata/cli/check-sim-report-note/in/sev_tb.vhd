entity sev_tb is
end entity;

architecture sim of sev_tb is
begin
  process
  begin
    wait for 1 ns;
    report "boom" severity note;
    report "after";
    wait;
  end process;
end architecture;
