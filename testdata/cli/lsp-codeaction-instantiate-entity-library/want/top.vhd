-- top: instantiates fifo of library ip, which the unit does not name
-- yet; DEPTH is a constant of the architecture (#57).
library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk  : in std_logic;
    din  : in std_logic;
    dout : out std_logic
  );
end entity top;

library ip;

architecture rtl of top is
  constant DEPTH : natural := 4;
begin
  u_fifo : entity ip.fifo
    generic map (
      DEPTH => DEPTH
    )
    port map (
      clk  => clk,
      din  => din,
      dout => dout,
      full => open
    );
end architecture rtl;
