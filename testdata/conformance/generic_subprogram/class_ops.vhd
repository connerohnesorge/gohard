-- A VHDL-2019 generic type of a class has the implicit operations of
-- its class (LRM 6.5.3): arithmetic and relational operators of
-- range <>, range <> . <> and units <>, the relational operators and
-- MAXIMUM of (<>) and <>, and .all and DEALLOCATE of an access class.
-- Instances made by a VHDL-2008 style declaration and by VHDL-2019
-- calls with a generic map.
entity class_ops_tb is
end entity class_ops_tb;

architecture sim of class_ops_tb is
  function dbl generic (type t is range <>) (x : t) return t is
  begin
    return x + x;
  end function dbl;

  function lt generic (type t is range <>) (x, y : t) return boolean is
  begin
    return x < y;
  end function lt;

  function sq generic (type t is range <>) (x : t) return t is
  begin
    return x * x - abs(x) / 2;
  end function sq;

  function phy generic (type t is units <>) (x : t) return t is
  begin
    return x + x;
  end function phy;

  function flt generic (type t is range <> . <>) (x : t) return t is
  begin
    return x * x + x;
  end function flt;

  function mx generic (type t is (<>)) (x, y : t) return t is
  begin
    if x > y then
      return x;
    end if;
    return maximum(x, y);
  end function mx;

  function le generic (type t is <>) (x, y : t) return boolean is
  begin
    return x <= y;
  end function le;

  procedure take generic (type d is private; type t is access d)
    (variable p : inout t; variable r : out d) is
  begin
    r := p.all;
    deallocate(p);
  end procedure take;

  function dbl_int is new dbl generic map (t => integer);
  type int_ptr is access integer;
  type color is (red, green, blue);
begin
  check : process
    variable p : int_ptr := new integer'(9);
    variable r : integer;
  begin
    assert dbl_int(21) = 42;
    assert dbl generic map (t => integer) (5) = 10;
    report "dbl = " & integer'image(dbl_int(21));
    assert lt generic map (t => integer) (1, 2);
    assert sq generic map (t => integer) (6) = 33;
    report "sq = " & integer'image(sq generic map (t => integer) (6));
    assert phy generic map (t => time) (2 ns) = 4 ns;
    report "phy = " & time'image(phy generic map (t => time) (2 ns));
    assert flt generic map (t => real) (1.5) = 3.75;
    report "flt = " & real'image(flt generic map (t => real) (1.5));
    assert mx generic map (t => color) (green, red) = green;
    report "mx = " & color'image(mx generic map (t => color) (green, red));
    assert le generic map (t => character) ('a', 'b');
    take generic map (d => integer, t => int_ptr) (p, r);
    assert r = 9 and p = null;
    report "take = " & integer'image(r) & " " & boolean'image(p = null);
    wait;
  end process check;
end architecture sim;
