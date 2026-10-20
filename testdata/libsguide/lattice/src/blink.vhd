library ieee;
use ieee.std_logic_1164.all;

library ecp5u;
use ecp5u.components.all;

entity blink is
  port (
    d : in  std_logic;
    q : out std_logic
  );
end entity blink;

architecture rtl of blink is
  signal osc : std_logic;
begin
  u_osc : OSCG
    generic map (DIV => 64)
    port map (OSC => osc);

  u_ff : FD1S3AX
    port map (D => d, CK => osc, Q => q);
end architecture rtl;
