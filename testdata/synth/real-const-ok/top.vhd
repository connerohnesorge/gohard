library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
use ieee.math_real.all;

entity top is
  generic (
    DEPTH : positive := 100
  );
  port (
    clk : in  std_ulogic;
    rst : in  std_ulogic;
    q   : out unsigned(integer(ceil(log2(real(DEPTH)))) - 1 downto 0)
  );
end entity;

architecture rtl of top is
  -- real appears only in constant expressions evaluated while elaborating.
  constant W : natural := integer(ceil(log2(real(DEPTH))));
  signal count : unsigned(W - 1 downto 0);
begin
  reg : process (clk)
  begin
    if rising_edge(clk) then
      if rst = '1' then
        count <= (others => '0');
      else
        count <= count + 1;
      end if;
    end if;
  end process;

  q <= count;
end architecture;
