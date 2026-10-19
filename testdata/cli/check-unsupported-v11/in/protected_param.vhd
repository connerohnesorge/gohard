use work.counter.all;

package protected_param is
  impure function peek (variable c : inout counter_t) return integer;
end package protected_param;
