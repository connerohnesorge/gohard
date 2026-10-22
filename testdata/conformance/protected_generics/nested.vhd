-- Protected types built from generic protected types (VHDL-2019,
-- LRM 5.6.2): the body of a generic protected type declares an object
-- of another one whose generic map reads the outer object's generics
-- (a constant and a type), so each outer object holds an inner object
-- with its own bindings. NVC 1.23 does not parse protected generics,
-- so there is no NVC recording.
entity nested_tb is
end entity nested_tb;

architecture sim of nested_tb is
  type cell_t is protected
    generic (step : integer; type elem);
    procedure put (x : elem);
    impure function count return integer;
    impure function last return elem;
  end protected cell_t;

  type cell_t is protected body
    variable n : integer := 0;
    variable l : elem;

    procedure put (x : elem) is
    begin
      l := x;
      n := n + step;
    end procedure put;

    impure function count return integer is
    begin
      return n;
    end function count;

    impure function last return elem is
    begin
      return l;
    end function last;
  end protected body cell_t;

  type pair_t is protected
    generic (k : integer; type t);
    procedure put (x : t);
    impure function count return integer;
  end protected pair_t;

  type pair_t is protected body
    variable inner : cell_t generic map (step => k * 10, elem => t);

    procedure put (x : t) is
    begin
      inner.put(x);
    end procedure put;

    impure function count return integer is
    begin
      return inner.count + k;
    end function count;
  end protected body pair_t;
begin
  check : process
    variable x : pair_t generic map (k => 2, t => integer);
    variable y : pair_t generic map (k => 4, t => character);
    variable i : integer := 5;
    variable c : character := 'q';
  begin
    x.put(i);
    y.put(c);
    y.put(c);
    report "x.count = " & integer'image(x.count) & ", y.count = " &
      integer'image(y.count);
    assert x.count = 22 and y.count = 84;
    wait;
  end process check;
end architecture sim;
