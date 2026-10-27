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
  -- The 1076.6 concurrent register form: a clock edge as the condition
  -- of a conditional assignment without else is a flip-flop.
  q <= d when rising_edge(clk);
end architecture;
