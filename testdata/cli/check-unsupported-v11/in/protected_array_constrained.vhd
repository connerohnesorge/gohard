use work.counter.all;

-- A constrained array type declares a subtype of an anonymous base.
package protected_array_constrained is
  type counter_pair_t is array (0 to 1) of counter_t;
end package protected_array_constrained;
