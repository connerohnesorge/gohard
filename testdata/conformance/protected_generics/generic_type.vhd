-- A generic type in a protected type's generic clause (VHDL-2019,
-- LRM 5.6.2, 6.5.3): each object binds it to its own actual, for its
-- variables, its methods' parameters and results, and the class
-- operations of a range class type. NVC 1.23 does not parse protected
-- generics, so there is no NVC recording.
entity generic_type_tb is
end entity generic_type_tb;

architecture sim of generic_type_tb is
  type acc_t is protected
    generic (type num is range <>; type item);
    procedure add (x : num);
    impure function total return num;
    procedure keep (x : item);
    impure function kept return item;
  end protected acc_t;

  type acc_t is protected body
    variable sum : num := num'low;
    variable last : item;

    procedure add (x : num) is
    begin
      sum := sum + x * 2;
    end procedure add;

    impure function total return num is
    begin
      return sum;
    end function total;

    procedure keep (x : item) is
    begin
      last := x;
    end procedure keep;

    impure function kept return item is
    begin
      return last;
    end function kept;
  end protected body acc_t;

  type small_t is range 10 to 100;
begin
  check : process
    variable a : acc_t generic map (num => integer, item => character);
    variable b : acc_t generic map (num => small_t, item => boolean);
    variable n : small_t := 20;
    variable c : character := 'x';
    variable t : boolean := true;
  begin
    a.add(20);
    a.add(1);
    b.add(n);
    a.keep(c);
    b.keep(t);
    report "a.total = " & integer'image(a.total) & ", b.total = " &
      small_t'image(b.total);
    report "a.kept = " & character'image(a.kept) & ", b.kept = " &
      boolean'image(b.kept);
    assert a.total = integer'low + 42 and b.total = 50;
    wait;
  end process check;
end architecture sim;
