library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

-- Width-generic unsigned adder.
entity adder is
  generic (WIDTH : natural := 8);
  port (
    a   : in  unsigned(WIDTH - 1 downto 0);
    b   : in  unsigned(WIDTH - 1 downto 0);
    sum : out unsigned(WIDTH - 1 downto 0)
  );
end entity adder;

architecture rtl of adder is
begin
  sum <= a + b;
end architecture rtl;
