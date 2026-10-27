library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    d : in  std_ulogic;
    q : out std_ulogic
  );
end entity;

architecture rtl of top is
begin
  sample : process
  begin
    wait for 10 ns;
    q <= d;
  end process;
end architecture;
