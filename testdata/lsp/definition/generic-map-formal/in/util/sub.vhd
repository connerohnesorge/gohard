library ieee;
use ieee.std_logic_1164.all;

-- A buffer stage.
entity sub is
  generic (DEPTH : natural := 4);
  port (
    -- The input bit.
    a : in  std_logic;
    y : out std_logic
  );
end entity sub;

architecture rtl of sub is
begin
  y <= a;
end architecture rtl;
