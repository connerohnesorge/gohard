vunit v_dut(dut(rtl)) {
  default clock is rising_edge(clk);
  assert always (a -> b);
}
