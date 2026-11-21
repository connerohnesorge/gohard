package maths is
  -- Limits value to at most limit.
  function clip(value : integer; limit : integer) return integer;
end package;

package body maths is
  function clip(value : integer; limit : integer) return integer is
  begin
    return value;
  end function;
end package body;

entity e is
end entity;

use work.maths.all;
architecture a of e is
  constant k : integer := clip(300, 255);
begin
end architecture;
