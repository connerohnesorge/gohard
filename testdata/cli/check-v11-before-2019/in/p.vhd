package p is
  type counter_t is protected
    procedure inc;
    alias bump is inc [];
  end protected counter_t;
  type counter_pair_t is array (0 to 1) of counter_t;
  type int_ptr is access integer;
  function peek (p : int_ptr) return integer;
end package p;
