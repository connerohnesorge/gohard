-- Objects of an array-class generic type (LRM 6.5.3) are indexed and
-- sliced with the generic type's index subtype, in an elaboration-time
-- constant, in a function and through a VHDL-2008 style instance, and
-- in a procedure that waits between reading and writing its inout
-- array; the element type may itself be a generic type.
entity array_generic_tb is
end entity array_generic_tb;

architecture sim of array_generic_tb is
  type iv is array (natural range <>) of integer;

  function sum
    generic (type t is array (natural range <>) of integer)
    parameter (a : t) return integer is
    variable s : integer := 0;
  begin
    for i in a'range loop
      s := s + a(i);
    end loop;
    return 100 * s + a(a'left to a'left + 1)(a'left + 1);
  end function sum;

  function upd
    generic (type el; type t is array (natural range <>) of el)
    parameter (a : t; x : el) return t is
    variable r : t(a'range) := a;
  begin
    r(r'left) := x;
    return r;
  end function upd;

  procedure swp
    generic (type t is array (natural range <>) of integer)
    parameter (variable a : inout t) is
    variable tmp : integer;
  begin
    tmp := a(a'left);
    wait for 1 ns;
    a(a'left) := a(a'right);
    a(a'right) := tmp;
  end procedure swp;

  function sumi is new sum generic map (t => iv);
  constant k : integer := sumi((4, 5));
begin
  check : process
    variable v : iv(0 to 2) := (9, 8, 7);
    variable s : integer;
  begin
    report "k = " & integer'image(k);
    assert k = 905 severity failure;
    s := sum generic map (t => iv) (v);
    report "sum = " & integer'image(s);
    assert s = 2408 severity failure;
    v := upd generic map (el => integer, t => iv) (v, 1);
    report "upd = " & integer'image(v(0));
    assert v = (1, 8, 7) severity failure;
    swp generic map (t => iv) (v);
    report "swp = " & integer'image(v(0)) & " " & integer'image(v(2));
    assert v = (7, 8, 1) severity failure;
    wait;
  end process check;
end architecture sim;
