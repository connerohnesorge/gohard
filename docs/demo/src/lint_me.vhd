library ieee;
use ieee.std_logic_1164.all;

entity lint_me is
  port (
    clk : in  std_logic;
    en  : in  std_logic;
    a   : in  std_logic;
    b   : in  std_logic;
    y   : out std_logic;
    q   : out std_logic
  );
end entity lint_me;

architecture rtl of lint_me is
  signal spare : std_logic;
begin
  comb : process (a)
  begin
    if en = '1' then
      y <= a;
    end if;
  end process;

  reg : process (clk)
  begin
    if rising_edge(clk) then
      q <= a;
    end if;
  end process;
end architecture rtl;
