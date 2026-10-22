-- Protected type instantiation (VHDL-2019, LRM 5.6.4): a type
-- instantiated from a generic protected type with a generic map binds
-- the generics of each of its objects, as a variable, as elements of
-- an array and through a pointer; a protected type with a generic map
-- in its header is instantiated in place. NVC 1.23 does not parse
-- protected type instantiations, so there is no NVC recording.
entity instance_tb is
end entity instance_tb;

architecture sim of instance_tb is
  type scaler_t is protected
    generic (factor : integer; offset : integer := 0);
    impure function apply (x : integer) return integer;
  end protected scaler_t;

  type scaler_t is protected body
    function scale (x : integer) return integer is
    begin
      return x * factor + offset;
    end function scale;

    impure function apply (x : integer) return integer is
    begin
      return scale(x);
    end function apply;
  end protected body scaler_t;

  type triple_t is new scaler_t generic map (factor => 3);
  type triple_arr is array (0 to 1) of triple_t;
  type triple_ptr is access triple_t;
  type shifted_t is new scaler_t generic map (factor => 1, offset => 100);

  type fixed_t is protected
    generic (base : integer);
    generic map (base => 7);
    impure function get return integer;
  end protected fixed_t;

  type fixed_t is protected body
    impure function get return integer is
    begin
      return base;
    end function get;
  end protected body fixed_t;
begin
  check : process
    variable t : triple_t;
    variable s : shifted_t;
    variable arr : triple_arr;
    variable p : triple_ptr;
    variable f : fixed_t;
  begin
    p := new triple_t;
    for i in 1 to 2 loop
      report "t = " & integer'image(t.apply(10)) & ", s = " &
        integer'image(s.apply(10)) & ", arr(1) = " &
        integer'image(arr(1).apply(i)) & ", p = " &
        integer'image(p.apply(i)) & ", f = " & integer'image(f.get);
    end loop;
    assert t.apply(10) = 30 and s.apply(10) = 110 and f.get = 7;
    deallocate(p);
    wait;
  end process check;
end architecture sim;
