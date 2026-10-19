-- The step limit per instance (#34): the constant of leaf needs about
-- N steps, so u_small is checked and u_big, with N = 3,000,000, reaches
-- the 1,000,000-step limit: a warning, and the instance is listed in
-- "incomplete".
package util is
  function count (n : natural) return natural;
end package util;

package body util is
  function count (n : natural) return natural is
    variable r : natural := 0;
  begin
    for i in 1 to n loop
      r := r + 1;
    end loop;
    return r;
  end function count;
end package body util;

use work.util.all;

entity leaf is
  generic (N : natural := 4);
end entity leaf;

architecture rtl of leaf is
  constant TOTAL : natural := count(N);
begin
end architecture rtl;

entity top is
end entity top;

architecture rtl of top is
begin
  u_small : entity work.leaf generic map (N => 10);
  u_big : entity work.leaf generic map (N => 3_000_000);
end architecture rtl;
