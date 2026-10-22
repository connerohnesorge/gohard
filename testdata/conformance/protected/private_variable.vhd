-- Private variable declarations (VHDL-2019, LRM 5.6.2): a private
-- variable in a protected type declaration is an object of each
-- protected object, created before the body's variables, and the
-- methods of the type reach it; here it is itself of a protected type.
entity private_tb is
end entity private_tb;

architecture sim of private_tb is
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

  type stats_t is protected
    private variable hits : counter_t;
    procedure hit;
    impure function total return integer;
  end protected stats_t;

  type stats_t is protected body
    variable scale : integer := 10;

    procedure hit is
    begin
      hits.add(scale);
    end procedure hit;

    impure function total return integer is
    begin
      return hits.get;
    end function total;
  end protected body stats_t;

  shared variable s1, s2 : stats_t;
begin
  check : process
  begin
    s1.hit;
    s1.hit;
    s2.hit;
    report "s1 = " & integer'image(s1.total) & ", s2 = " &
      integer'image(s2.total);
    assert s1.total = 20 and s2.total = 10 report "wrong" severity error;
    wait;
  end process check;
end architecture sim;
