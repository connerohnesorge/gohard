-- fifo: an entity of library ip whose generic has no default.
library ieee;
use ieee.std_logic_1164.all;

entity fifo is
  generic (
    DEPTH : natural
  );
  port (
    clk  : in std_logic;
    din  : in std_logic;
    dout : out std_logic;
    full : out std_logic
  );
end entity fifo;

architecture rtl of fifo is
begin
  dout <= din when rising_edge(clk);
  full <= '1' when DEPTH = 0 else '0';
end architecture rtl;
