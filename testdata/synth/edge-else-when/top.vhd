library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk : in  std_ulogic;
    d   : in  std_ulogic;
    q   : out std_ulogic
  );
end entity;

architecture rtl of top is
begin
  -- The concurrent form of edge-else: an else branch after the edge.
  q <= d when rising_edge(clk) else '0';
end architecture;
