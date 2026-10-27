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
  -- Two clock-edge waits in one process: an implicit two-state machine.
  twice : process
  begin
    wait until rising_edge(clk);
    q <= d;
    wait until rising_edge(clk);
    q <= not d;
  end process;
end architecture;
