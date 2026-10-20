package p1 is
  function scale (x : integer) return integer;
end package p1;

package p2 is
  function scale (x : integer) return integer;
  function scale (b : boolean) return integer;
end package p2;

use work.p1.all;
use work.p2.all;

entity amb is
end entity amb;

architecture rtl of amb is
  signal a : integer := scale(3);
  signal b : integer := scale(true);
  signal c : integer := scale('x');
begin
end architecture rtl;
