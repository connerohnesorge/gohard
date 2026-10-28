vunit v(dut(rtl)) {
  default clock is rising_edge(clk);
  f1 : assert follows(a, b) report "f1: b did not follow a";
  f2 : assert (follows(a, b)) report "f2: b did not follow a";
  c1 : cover {(pair(a, b))} report "c1: a then b";
}
