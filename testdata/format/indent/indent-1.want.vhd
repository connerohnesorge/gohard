library ieee;
use ieee.std_logic_1164.all;
entity counter is
 generic (WIDTH : natural := 8);
 port (clk : in std_logic;
  rst : in std_logic;
  q   : out std_logic_vector(WIDTH - 1 downto 0));
end entity counter;
architecture rtl of counter is
 type state_t is (idle, run);
 type pair_t is record
  lo : natural;
  hi : natural;
 end record;
 signal count : natural range 0 to 2 ** WIDTH - 1;
 function next_of(x : natural) return natural is
 begin
  return x + 1;
 end function;
begin
 counting : process (clk) is
  variable v : natural;
 begin
  if rising_edge(clk) then
   if rst = '1' then
    count <= 0;
   elsif count = 2 ** WIDTH - 1 then
    count <= 0;
   else
    count <= next_of(count);
   end if;
   case count is
    when 0 =>
     v := 1;
    when others =>
     for i in 0 to 3 loop
      v := v + i;
     end loop;
   end case;
  end if;
 end process counting;
 gen : for i in 0 to WIDTH - 1 generate
  q(i) <= '1' when count mod 2 = 1 else '0';
 end generate gen;
end architecture rtl;
