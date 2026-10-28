-- top: a component instantiation of counter without its component
-- declaration (#57).
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity top is
  port (
    clk   : in std_logic;
    rst   : in std_logic;
    count : out unsigned(7 downto 0)
  );
end entity top;

architecture rtl of top is
  signal enable : std_logic;

  component counter is
    generic (
      WIDTH : positive := 8
    );
    port (
      clk   : in std_logic;
      rst   : in std_logic;
      en    : in std_logic := '1';
      count : out unsigned(WIDTH - 1 downto 0)
    );
  end component counter;
begin
  enable <= '1';

  u0 : counter
    port map (
      clk   => clk,
      rst   => rst,
      en    => enable,
      count => count
    );
end architecture rtl;
