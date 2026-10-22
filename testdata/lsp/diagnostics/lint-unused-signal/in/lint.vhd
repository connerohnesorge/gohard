library ieee;
use ieee.std_logic_1164.all;

entity sub is
  port (i : in std_logic; o : out std_logic);
end entity;

architecture rtl of sub is
begin
  o <= i;
end architecture;

library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (clk, unused_in : in std_logic; q, dangling : out std_logic);
end entity;

architecture rtl of top is
  type pair_t is record
    a, b : std_logic;
  end record;
  procedure drive (signal x : out std_logic) is
  begin
    x <= '1';
  end procedure;
  -- Findings.
  signal s_read_only  : std_logic := '0';
  signal s_write_only : std_logic;
  signal s_dead       : std_logic;
  -- No findings: driven by an instance, a procedure or a field.
  signal s_ok, s_from_inst, s_from_proc : std_logic;
  signal s_pair                         : pair_t;
begin
  u : entity work.sub port map (i => s_ok, o => s_from_inst);
  drive(s_from_proc);
  s_pair.a <= s_from_proc;
  s_pair.b <= s_from_inst;
  process (clk)
  begin
    if rising_edge(clk) then
      s_ok         <= s_read_only;
      s_write_only <= '1';
      q            <= s_pair.a xor s_pair.b;
    end if;
  end process;
end architecture;
