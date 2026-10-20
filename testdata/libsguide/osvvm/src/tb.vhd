library osvvm;
context osvvm.OsvvmContext;

entity tb is
end entity tb;

architecture sim of tb is
begin
  process
    variable rv : RandomPType;
  begin
    SetAlertLogName("tb");
    rv.InitSeed(1);
    AffirmIf(rv.RandInt(0, 3) <= 3, "range");
    EndOfTestReports;
    wait;
  end process;
end architecture sim;
