library ieee;
use ieee.std_logic_1164.all;

entity both is
  port (
    a  : in  std_logic;
    en : in  std_logic;
    b  : in  std_logic;
    y  : out std_logic
  );
end entity both;

architecture rtl of both is
  signal spare : std_logic;
begin
  comb : process (a)
  begin
    if en = '1' then
      y <= a;
    end if;
  end process;

  assert missing = '1';
end architecture rtl;
