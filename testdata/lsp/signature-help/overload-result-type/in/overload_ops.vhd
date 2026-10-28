-- overload_ops: the use clause makes ow_sat's operators visible; the
-- wrapping "+" is reached only through its expanded name. With
-- `use work.ow_wrap.all` instead, a + b would run ow_wrap's body.
use work.ow_types.all;
use work.ow_sat.all;

entity overload_ops is
  port (
    a        : in  word_t;
    b        : in  word_t;
    sum_ww   : out word_t;
    sum_wi   : out word_t;
    sum_wrap : out word_t;
    neg_w    : out word_t;
    neg_i    : out integer
  );
end entity overload_ops;

architecture rtl of overload_ops is
begin
  -- Resolved by parameter types.
  sum_ww <= a + b;
  sum_wi <= a + 100;
  -- Resolved by an expanded name.
  sum_wrap <= work.ow_wrap."+"(a, b);
  -- Resolved by result type (the target's type).
  neg_w <= -a;
  neg_i <= -a;
end architecture rtl;
