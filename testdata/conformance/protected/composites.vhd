-- nvc-differs: NVC 1.23 rejects arrays and records of protected types
-- Arrays and records of protected types (VHDL-2019): every element
-- and field is its own protected object, created when the composite
-- is elaborated; method calls go through indexed and selected names,
-- also of an array of pointers to protected objects.
entity composites_tb is
end entity composites_tb;

architecture sim of composites_tb is
  type counter_t is protected
    procedure add(by : integer);
    impure function get return integer;
  end protected counter_t;

  type counter_t is protected body
    variable n : integer := 0;

    procedure add(by : integer) is
    begin
      n := n + by;
    end procedure add;

    impure function get return integer is
    begin
      return n;
    end function get;
  end protected body counter_t;

  type counters_t is array (natural range <>) of counter_t;

  type pair_t is record
    lo, hi : counter_t;
  end record pair_t;

  type nest_t is record
    p   : pair_t;
    arr : counters_t(1 to 2);
  end record nest_t;

  type counter_ptr is access counter_t;
  type ptrs_t is array (0 to 1) of counter_ptr;
begin
  check : process
    variable bank  : counters_t(0 to 3);
    variable nest  : nest_t;
    variable local : counters_t(0 to 1);
    variable ps    : ptrs_t;
    variable pr    : pair_t;
    variable sum   : integer := 0;
  begin
    for i in bank'range loop
      bank(i).add(i * 10);
    end loop;
    bank(2).add(5);
    nest.p.hi.add(7);
    nest.arr(2).add(9);
    nest.arr(2).add(1);
    local(1).add(4);
    pr.lo.add(3);
    ps(1) := new counter_t;
    ps(1).add(8);
    ps(1).all.add(1);
    for i in bank'range loop
      sum := sum + bank(i).get;
    end loop;
    report "bank(2) = " & integer'image(bank(2).get) & ", sum = " &
      integer'image(sum);
    report "nest = " & integer'image(nest.p.lo.get) & " " &
      integer'image(nest.p.hi.get) & " " &
      integer'image(nest.arr(1).get) & " " &
      integer'image(nest.arr(2).get);
    report "local = " & integer'image(local(0).get) & " " &
      integer'image(local(1).get) & ", pr = " &
      integer'image(pr.lo.get) & " " & integer'image(pr.hi.get);
    assert bank(2).get = 25 and sum = 65 report "bank" severity error;
    assert nest.p.hi.get = 7 and nest.arr(2).get = 10
      report "nest" severity error;
    report "ps(1) = " & integer'image(ps(1).get) & ", ps(0) is null: " &
      boolean'image(ps(0) = null);
    assert local(1).get = 4 and pr.lo.get = 3 and pr.hi.get = 0
      report "local" severity error;
    assert ps(1).get = 9 report "ps" severity error;
    wait;
  end process check;
end architecture sim;
