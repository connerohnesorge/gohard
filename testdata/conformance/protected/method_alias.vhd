-- nvc-differs: NVC 1.23 crashes analyzing a method alias (tree kind T_PROT_REF does not have item I_PORTS)
-- Method aliases (VHDL-2019, LRM 5.6.2, 6.6.1): an alias in a
-- protected type declaration of a method of a private variable calls
-- that method on the private variable of the object the alias is
-- called on, from outside and from the type's own body.
entity method_alias_tb is
end entity method_alias_tb;

architecture sim of method_alias_tb is
  type counter_t is protected
    procedure incr (n : integer);
    impure function get return integer;
  end protected counter_t;

  type counter_t is protected body
    variable count : integer := 0;

    procedure incr (n : integer) is
    begin
      count := count + n;
    end procedure incr;

    impure function get return integer is
    begin
      return count;
    end function get;
  end protected body counter_t;

  type stats_t is protected
    private variable hits : counter_t;
    alias hit is hits.incr [integer];
    alias hit_count is hits.get [return integer];
    procedure hit_twice;
  end protected stats_t;

  type stats_t is protected body
    procedure hit_twice is
    begin
      hit(1);
      hit(1);
    end procedure hit_twice;
  end protected body stats_t;

  shared variable a, b : stats_t;
begin
  p : process
  begin
    a.hit(5);
    b.hit(1);
    a.hit_twice;
    report "a.hit_count = " & integer'image(a.hit_count);
    report "b.hit_count = " & integer'image(b.hit_count);
    assert a.hit_count = 7 and b.hit_count = 1
      report "method aliases" severity failure;
    wait;
  end process p;
end architecture sim;
