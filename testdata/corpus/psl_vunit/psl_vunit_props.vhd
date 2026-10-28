-- psl_vunit_checks: an inline PSL verification unit bound to
-- psl_vunit(rtl) (#66). Its names are those of the bound design entity:
-- the port clk and the architecture's signals reqi and acki. Its
-- directives run in every instance of psl_vunit(rtl) (here
-- psl_vunit_tb.dut, as psl_vunit_tb.dut.psl_vunit_checks@work.ack_next
-- and .req_ack).
--   assert always (req -> next ack)  holds at k = 2, fails at k = 6
--                                    (65 ns), by design
--   cover {req; ack}                 hits at k = 3 (35 ns)
vunit psl_vunit_checks(psl_vunit(rtl)) {
  default clock is rising_edge(clk);

  ack_next : assert always (reqi = '1' -> next acki = '1')
    report "ack did not follow req";

  req_ack : cover {reqi = '1'; acki = '1'}
    report "req then ack";
}
