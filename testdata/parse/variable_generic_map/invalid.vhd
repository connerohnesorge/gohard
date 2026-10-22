-- test: std=2008
architecture a of e is
begin
  process is
    variable v : pt generic map (n => 8);
  begin
    wait;
  end process;
end;
