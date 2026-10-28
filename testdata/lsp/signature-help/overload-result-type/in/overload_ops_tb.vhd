-- overload_ops_tb: self-checking; each output's src tag names the body
-- that ran. Stops with std.env.finish.
use std.env.all;
use work.ow_types.all;

entity overload_ops_tb is
end entity overload_ops_tb;

architecture sim of overload_ops_tb is
  signal a        : word_t := w(0);
  signal b        : word_t := w(0);
  signal sum_ww   : word_t;
  signal sum_wi   : word_t;
  signal sum_wrap : word_t;
  signal neg_w    : word_t;
  signal neg_i    : integer;
begin
  dut : entity work.overload_ops
    port map (
      a        => a,
      b        => b,
      sum_ww   => sum_ww,
      sum_wi   => sum_wi,
      sum_wrap => sum_wrap,
      neg_w    => neg_w,
      neg_i    => neg_i
    );

  check : process
  begin
    a <= w(200);
    b <= w(100);
    wait for 1 ns;
    assert sum_ww = (v => 255, src => 'S')
      report "a + b did not run ow_sat word+word" severity failure;
    assert sum_wi = (v => 255, src => 'I')
      report "a + 100 did not run ow_sat word+integer" severity failure;
    assert sum_wrap = (v => 44, src => 'W')
      report "ow_wrap.""+"" did not run ow_wrap" severity failure;
    assert neg_w = (v => 55, src => 'N')
      report "-a (word) did not run ow_sat ""-"" return word"
      severity failure;
    assert neg_i = -200
      report "-a (integer) did not run ow_sat ""-"" return integer"
      severity failure;
    finish;
  end process check;
end architecture sim;
