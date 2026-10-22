-- Access types to protected types (VHDL-2019): `new counter_t`
-- creates a protected object; method calls go through p.all and the
-- implicit dereference p.method; deallocate frees the object.
entity pointers_tb is
end entity pointers_tb;

architecture sim of pointers_tb is
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

  type counter_ptr is access counter_t;
begin
  check : process
    variable p, q, r : counter_ptr;
  begin
    p := new counter_t;
    p.all.add(2);
    p.add(3);
    q := p;
    q.add(1);
    r := new counter_t;
    r.all.add(8);
    report "p = " & integer'image(p.get) & ", r = " &
      integer'image(r.all.get);
    assert p.get = 6 and r.get = 8 report "wrong counts" severity error;
    deallocate(p);
    assert p = null report "not null" severity error;
    report "q is not null: " & boolean'image(q /= null);
    wait;
  end process check;
end architecture sim;
