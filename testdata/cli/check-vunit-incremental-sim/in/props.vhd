vunit v_comp {
  default clock is rising_edge(clk);
  comp_ab : assert always (a -> b) report "comp: a without b";
}
