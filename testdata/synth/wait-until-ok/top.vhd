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
  -- One clock-edge wait at the top of the process: a flip-flop.
  reg : process
  begin
    wait until rising_edge(clk);
    q <= d;
  end process;
end architecture;
