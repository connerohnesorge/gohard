-- The v1.0 fixture check-unsupported-v11/in/int64.vhd, now a v1.1
-- simulation: a 64-bit INTEGER constant.
package int64 is
  constant big : integer := 2 ** 40;
end package int64;

use work.int64.all;

entity int64_tb is
end entity int64_tb;

architecture sim of int64_tb is
begin
  process
  begin
    report "big = " & integer'image(big);
    assert big / 2 ** 20 = 2 ** 20 report "big" severity error;
    wait;
  end process;
end architecture sim;
