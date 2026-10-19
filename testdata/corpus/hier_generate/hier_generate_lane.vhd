-- hier_generate_lane: one lane of the hier_generate corpus design (#8).
-- Generic width W; the if-generate picks an inverting or a pass-through
-- body, so the instance path names the branch taken.
library ieee;
use ieee.std_logic_1164.all;

entity hier_generate_lane is
  generic (
    W      : positive;
    INVERT : boolean
  );
  port (
    a : in  std_logic_vector(W - 1 downto 0);
    y : out std_logic_vector(W - 1 downto 0)
  );
end entity hier_generate_lane;

architecture rtl of hier_generate_lane is
begin
  polarity : if inv : INVERT generate
    y <= not a;
  else pass : generate
    y <= a;
  end generate polarity;
end architecture rtl;
