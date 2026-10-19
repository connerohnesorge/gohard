-- crypt_core_model: plain VHDL stand-in for the opaque work.crypt_core
-- (simulation.models). Same entity and ports as crypt_core.vhd. Behaviour:
-- one clock after din is sampled, dout = din xor x"5A" and valid = '1';
-- reset clears both.
library ieee;
use ieee.std_logic_1164.all;

entity crypt_core is
  port (
    clk   : in  std_logic;
    rst   : in  std_logic;
    din   : in  std_logic_vector(7 downto 0);
    dout  : out std_logic_vector(7 downto 0);
    valid : out std_logic
  );
end entity crypt_core;

architecture model of crypt_core is
begin
  process (clk)
  begin
    if rising_edge(clk) then
      if rst = '1' then
        dout  <= (others => '0');
        valid <= '0';
      else
        dout  <= din xor x"5A";
        valid <= '1';
      end if;
    end if;
  end process;
end architecture model;
