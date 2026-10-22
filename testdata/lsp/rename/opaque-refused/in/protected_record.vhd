use work.counter.all;

package protected_record is
  type pair_t is record
    a, b : counter_t;
  end record pair_t;
  constant d : string := std.env.dir_workingdir;
end package protected_record;
