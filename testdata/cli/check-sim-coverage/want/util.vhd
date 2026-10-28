-- util: a pure function with an if-elsif-else and a resolution
-- function with a loop, both run by the evaluator (#62).
package util is
  function clamp (x, lo, hi : integer) return integer;
  function any_set (v : bit_vector) return bit;
  subtype wired is any_set bit;
  type wired_vector is array (natural range <>) of wired;
end package util;

package body util is
  function clamp (x, lo, hi : integer) return integer is
  begin
    if x < lo then
      return lo;
    elsif x > hi then
      return hi;
    else
      return x;
    end if;
  end function clamp;

  function any_set (v : bit_vector) return bit is
  begin
    for i in v'range loop
      if v(i) = '1' then
        return '1';
      end if;
    end loop;
    return '0';
  end function any_set;
end package body util;
