LIBRARY ieee;
USE ieee.Std_Logic_1164.ALL;
ENTITY MixedCase IS
  PORT (Clk : IN Std_Logic; DataOut : OUT Std_Logic);
END ENTITY MixedCase;
ARCHITECTURE Rtl OF MixedCase IS
  SIGNAL Toggle : Std_Logic := '0';
BEGIN
  DataOut <= Toggle AND Clk;
  Flip : PROCESS (Clk) IS
  BEGIN
    IF Rising_Edge(Clk) THEN
      Toggle <= NOT Toggle;
    END IF;
  END PROCESS Flip;
END ARCHITECTURE Rtl;
