entity div_tb is
end entity;

architecture sim of div_tb is
begin
  process
    variable zero : integer := 0;
  begin
    wait for 1 ns;
    report integer'image(1 / zero);
    wait;
  end process;
end architecture;
