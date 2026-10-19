use work.counter.all;

package protected_array is
  type counters_t is array (natural range <>) of counter_t;
end package protected_array;
