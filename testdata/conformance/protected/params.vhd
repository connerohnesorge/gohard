-- Protected-type and access parameters on functions (VHDL-2019):
-- a function takes a protected object (class variable) and calls its
-- methods; a function reads through an access parameter, and an
-- inout access parameter is written back to its actual.
entity params_tb is
end entity params_tb;

architecture sim of params_tb is
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

  type int_ptr is access integer;

  impure function bump (variable c : inout counter_t; by : integer)
    return integer is
  begin
    c.add(by);
    return c.get;
  end function bump;

  impure function peek (variable c : counter_t) return integer is
  begin
    return c.get * 2;
  end function peek;

  impure function deref (variable p : in int_ptr) return integer is
  begin
    return p.all + 1;
  end function deref;

  impure function fresh (variable p : inout int_ptr; v : integer)
    return integer is
  begin
    p := new integer'(v);
    return v;
  end function fresh;

  shared variable c : counter_t;
begin
  check : process
    variable p, q : int_ptr;
    variable r    : integer;
  begin
    r := bump(c, 4);
    r := bump(c, 5) + r;
    p := new integer'(41);
    report "bump = " & integer'image(r) & ", peek = " &
      integer'image(peek(c)) & ", deref = " & integer'image(deref(p));
    r := fresh(q, 7);
    report "fresh = " & integer'image(q.all);
    assert c.get = 9 and r = 7 and q.all = 7 report "wrong"
      severity error;
    wait;
  end process check;
end architecture sim;
