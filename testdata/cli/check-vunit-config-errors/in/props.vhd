vunit v_spec {
  default clock is rising_edge(clk);
  spec_pulse : cover {a; not a};
}

vunit v_bound(dut(rtl)) {
  default clock is rising_edge(clk);
  bound_ab : assert always (a -> b);
}
