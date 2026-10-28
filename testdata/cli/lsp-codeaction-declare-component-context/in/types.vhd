-- types: the type of counter's output (#57).
library ieee;
use ieee.std_logic_1164.all;

package types is
  subtype word_t is std_logic_vector(7 downto 0);
end package types;
