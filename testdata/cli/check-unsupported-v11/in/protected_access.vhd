use work.counter.all;

package protected_access is
  type counter_ptr is access counter_t;
end package protected_access;
