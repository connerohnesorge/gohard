library ieee;
use ieee.std_logic_1164.all;

entity leaf is
  port (i : in std_logic; o : out std_logic);
end entity;

architecture rtl of leaf is
begin
  o <= i;
end architecture;

library ieee;
use ieee.std_logic_1164.all;

entity clean is
  port (clk, a : in std_logic; io : inout std_logic;
        q, y : out std_logic);
end entity;

architecture rtl of clean is
  type pair_t is record
    x, w : std_logic;
  end record;
  procedure drive (signal s : out std_logic) is
  begin
    s <= '1';
  end procedure;
  signal from_leaf, from_proc, held : std_logic;
  signal pair                       : pair_t;
  alias held_a is held;
begin
  u : entity work.leaf port map (i => a, o => from_leaf);
  drive(from_proc);
  pair.x <= from_leaf;
  pair.w <= from_proc;
  io     <= 'Z';
  process (clk)
  begin
    if rising_edge(clk) then
      held_a <= pair.x xor pair.w xor io;
    end if;
  end process;
  q <= held;
  y <= held_a;
end architecture;
