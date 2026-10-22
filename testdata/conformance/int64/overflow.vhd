-- 64-bit INTEGER in VHDL-2019: adding one to INTEGER'HIGH overflows
-- at run time; the run fails at the addition.
entity overflow_tb is
end entity overflow_tb;

architecture sim of overflow_tb is
begin
  p : process
    variable v : integer := integer'high - 1;
  begin
    v := v + 1;
    report "v = " & integer'image(v);
    v := v + 1;
    report "not reached" severity failure;
    wait;
  end process p;
end architecture sim;
