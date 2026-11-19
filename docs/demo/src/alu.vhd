library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

-- Add the operands, or pass x through, chosen by op.
entity alu is
  port (
    op  : in  std_logic;
    x   : in  unsigned(7 downto 0);
    y   : in  unsigned(7 downto 0);
    res : out unsigned(7 downto 0)
  );
end entity alu;

architecture rtl of alu is
  -- Sum of the two operands.
  signal added : unsigned(7 downto 0);

  function pick(sel : std_logic; a, b : unsigned) return unsigned is
  begin
    if sel = '1' then
      return a;
    end if;
    return b;
  end function pick;
begin
  u_add : entity work.adder
    generic map (WIDTH => 8)
    port map (a => x, b => y, sum => added);

  res <= pick(op, added, x);
end architecture rtl;
