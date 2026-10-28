-- Unbound verification units: a verification unit binding indication
-- binds each to a design entity, whose names they then see.
vunit v_comp {
  default clock is rising_edge(clk);
  comp_ab : assert always (a -> b) report "comp: a without b";
}

vunit v_spec {
  default clock is rising_edge(clk);
  spec_pulse : cover {a; not a} report "spec: a pulse";
}

vunit v_top {
  default clock is rising_edge(clk);
  top_ab : cover {a; b} report "top: a then b";
}
