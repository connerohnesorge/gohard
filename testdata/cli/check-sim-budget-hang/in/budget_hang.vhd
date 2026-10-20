entity budget_hang is
end entity;

architecture sim of budget_hang is
  signal clk : bit := '0';
begin
  clk <= not clk after 5 ns;
end architecture;
