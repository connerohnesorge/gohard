-- case_generate: a 4-bit lane whose function a case generate picks by
-- the generic OP (#63). The alternatives cover a single choice, a
-- choice list, a range and others; op_xor has a declarative part of
-- its own. Instance paths: case_generate_tb.u<k>.pick, with the
-- alternative label op_and, op_or, op_xor or op_not.
library ieee;
use ieee.std_logic_1164.all;

entity case_generate is
  generic (
    OP : natural := 0
  );
  port (
    a : in  std_logic_vector(3 downto 0);
    b : in  std_logic_vector(3 downto 0);
    y : out std_logic_vector(3 downto 0)
  );
end entity case_generate;

architecture rtl of case_generate is
begin
  pick : case OP generate
    when op_and : 0 =>
      y <= a and b;
    when op_or : 1 | 2 =>
      y <= a or b;
    when op_xor : 3 to 5 =>
      signal t : std_logic_vector(3 downto 0);
    begin
      t <= a xor b;
      y <= t;
    end op_xor;
    when op_not : others =>
      y <= not a;
  end generate pick;
end architecture rtl;
