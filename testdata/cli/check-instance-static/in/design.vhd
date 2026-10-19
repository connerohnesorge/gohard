-- Per-instance static checks (#34): generics computed by functions
-- interpreted from source (a user clog2 and ieee.numeric_std), and
-- errors that depend on the instance's generics, each naming the
-- instance path and its generic values.
library ieee;
use ieee.numeric_std.all;

package util is
  function clog2 (n : positive) return natural;
end package util;

package body util is
  function clog2 (n : positive) return natural is
    variable r : natural := 0;
  begin
    while 2 ** r < n loop
      r := r + 1;
    end loop;
    return r;
  end function clog2;
end package body util;

use work.util.all;

entity fifo is
  generic (DEPTH : positive := 16; AW : natural := clog2(16));
  port (addr : in bit_vector(AW - 1 downto 0));
end entity fifo;

architecture rtl of fifo is
  constant LAST : natural := DEPTH - 2 ** AW + 1;
begin
end architecture rtl;

library ieee;
use ieee.numeric_std.all;
use work.util.all;

entity top is
end entity top;

architecture rtl of top is
  constant SIZES : integer_vector := (16, 12, 128);
  signal bus6 : bit_vector(5 downto 0);
begin
  g : for i in SIZES'range generate
    u : entity work.fifo
      generic map (DEPTH => SIZES(i), AW => clog2(SIZES(i)))
      port map (addr => bus6(clog2(SIZES(i)) - 1 downto 0));
  end generate g;
  w : entity work.fifo generic map (DEPTH => 8, AW => to_integer(
      to_unsigned(3, 4))) port map (addr => bus6);
end architecture rtl;
