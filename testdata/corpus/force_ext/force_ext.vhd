-- force_ext: process(all), force/release and external names.
--
-- Two process(all) statements: `comb` computes the internal signal mid
-- from a, b and op; `pick` selects one bit of mid with sel, so its
-- implicit sensitivity set must contain both mid and sel. The
-- testbench reaches mid and the constant C_MAGIC through external
-- names and forces and releases mid.
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity force_ext is
  port (
    a     : in  std_logic_vector(3 downto 0);
    b     : in  std_logic_vector(3 downto 0);
    op    : in  std_logic;
    sel   : in  std_logic_vector(1 downto 0);
    y     : out std_logic_vector(3 downto 0);
    bit_o : out std_logic
  );
end entity force_ext;

architecture rtl of force_ext is
  constant C_MAGIC : integer := 42;
  signal mid       : std_logic_vector(3 downto 0);
begin
  comb : process (all) is
  begin
    if op = '1' then
      mid <= a and b;
    else
      mid <= a or b;
    end if;
  end process comb;

  pick : process (all) is
  begin
    bit_o <= mid(to_integer(unsigned(sel)));
  end process pick;

  y <= mid;
end architecture rtl;
