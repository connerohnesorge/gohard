library ieee;
use ieee.Std_Logic_1164.all;
entity MixedCase is
  port (Clk : in Std_Logic; DataOut : out Std_Logic);
end entity MixedCase;
architecture Rtl of MixedCase is
  signal Toggle : Std_Logic := '0';
begin
  DataOut <= Toggle and Clk;
  Flip : process (Clk) is
  begin
    if Rising_Edge(Clk) then
      Toggle <= not Toggle;
    end if;
  end process Flip;
end architecture Rtl;
