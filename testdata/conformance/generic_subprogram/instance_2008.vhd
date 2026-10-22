-- std: 2008
-- 2008 subprogram instances (LRM 4.4): generic constants, generic
-- types and generic subprograms bound per instance.
package gen_pkg is
  function scale
    generic (k : integer)
    parameter (x : integer) return integer;
  function pick
    generic (type t; function better (a, b : t) return boolean)
    parameter (a, b : t) return t;
  procedure accumulate
    generic (step : integer := 1)
    parameter (variable acc : inout integer; n : in natural);
end package gen_pkg;

package body gen_pkg is
  function scale
    generic (k : integer)
    parameter (x : integer) return integer is
  begin
    return k * x;
  end function scale;

  function pick
    generic (type t; function better (a, b : t) return boolean)
    parameter (a, b : t) return t is
  begin
    if better(a, b) then
      return a;
    end if;
    return b;
  end function pick;

  procedure accumulate
    generic (step : integer := 1)
    parameter (variable acc : inout integer; n : in natural) is
  begin
    for i in 1 to n loop
      acc := acc + step;
    end loop;
  end procedure accumulate;
end package body gen_pkg;

use work.gen_pkg.all;

entity instance_2008_tb is
end entity instance_2008_tb;

architecture sim of instance_2008_tb is
  function triple is new scale generic map (k => 3);
  function double is new scale generic map (k => 2);
  function max_int is new pick generic map (t => integer, better => ">");
  function min_real is new pick generic map (t => real, better => "<");
  procedure add_one is new accumulate;
  procedure add_five is new accumulate generic map (step => 5);
  constant c : integer := triple(7);
begin
  p : process
    variable acc : integer := 0;
  begin
    report "triple(4) = " & integer'image(triple(4));
    report "double(4) = " & integer'image(double(4));
    report "c = " & integer'image(c);
    report "max_int = " & integer'image(max_int(3, 9));
    report "min_real = " & real'image(min_real(2.5, 1.5));
    add_one(acc, 3);
    add_five(acc, 2);
    report "acc = " & integer'image(acc);
    assert triple(4) = 12 and double(4) = 8 and c = 21
      severity failure;
    assert max_int(3, 9) = 9 and min_real(2.5, 1.5) = 1.5
      severity failure;
    assert acc = 13 severity failure;
    wait;
  end process p;
end architecture sim;
