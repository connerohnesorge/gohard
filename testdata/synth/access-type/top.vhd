library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk : in  std_ulogic;
    d   : in  std_ulogic;
    q   : out std_ulogic
  );
end entity;

architecture rtl of top is
begin
  reg : process (clk)
    type bit_ptr is access std_ulogic;
    variable p : bit_ptr;
  begin
    if rising_edge(clk) then
      if p = null then
        p := new std_ulogic;
      end if;
      p.all := d;
      q <= p.all;
    end if;
  end process;
end architecture;
