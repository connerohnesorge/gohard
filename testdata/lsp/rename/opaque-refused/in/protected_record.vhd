use work.counter.all;

package protected_record is
  type pair_t is record
    a, b : counter_t;
  end record pair_t;
  constant d : boolean := std.env.PslIsCovered;
end package protected_record;
