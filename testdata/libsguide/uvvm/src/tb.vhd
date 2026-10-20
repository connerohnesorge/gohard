library uvvm_util;
context uvvm_util.uvvm_util_context;

entity tb is
end entity tb;

architecture sim of tb is
begin
  process
  begin
    log(ID_LOG_HDR, "start");
    check_value(1 = 1, ERROR, "one");
    report_alert_counters(FINAL);
    std.env.stop;
    wait;
  end process;
end architecture sim;
