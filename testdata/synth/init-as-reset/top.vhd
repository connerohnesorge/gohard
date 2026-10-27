library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity top is
  port (
    clk : in  std_ulogic;
    q   : out unsigned(3 downto 0)
  );
end entity;

architecture rtl of top is
  -- The only "reset" is the initial value.
  signal count : unsigned(3 downto 0) := "0101";
begin
  reg : process (clk)
  begin
    if rising_edge(clk) then
      count <= count + 1;
    end if;
  end process;

  q <= count;
end architecture;
