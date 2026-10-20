library ieee;
use ieee.std_logic_1164.all;

library unisim;
use unisim.vcomponents.all;

library unimacro;
use unimacro.vcomponents.all;

entity top is
  port (
    clk_p, clk_n : in  std_logic;
    d            : in  std_logic;
    q            : out std_logic;
    tc           : out std_logic
  );
end entity top;

architecture rtl of top is
  signal clk_in, clk : std_logic;
  signal count       : std_logic_vector(15 downto 0);
begin
  u_ibuf : IBUFDS
    generic map (DIFF_TERM => true)
    port map (O => clk_in, I => clk_p, IB => clk_n);

  u_bufg : BUFG
    port map (O => clk, I => clk_in);

  u_ff : FDRE
    port map (Q => q, C => clk, CE => '1', D => d, R => '0');

  u_count : COUNTER_TC_MACRO
    generic map (WIDTH_DATA => 16, TC_VALUE => x"00000000FFFF")
    port map (Q => count, TC => tc, CE => '1', CLK => clk, RST => '0');
end architecture rtl;
