-- case_generate_tb: one instance per alternative of case_generate (OP
-- = 0, 2, 4 and 9), checked against the function each alternative
-- computes. Ends with std.env.finish.
library ieee;
use ieee.std_logic_1164.all;

entity case_generate_tb is
end entity case_generate_tb;

architecture sim of case_generate_tb is
  type vec_array is array (natural range <>)
    of std_logic_vector(3 downto 0);
  constant as : vec_array := ("0000", "1100", "1010", "1111");
  constant bs : vec_array := ("0000", "1010", "0110", "0101");
  signal a  : std_logic_vector(3 downto 0) := (others => '0');
  signal b  : std_logic_vector(3 downto 0) := (others => '0');
  signal y0 : std_logic_vector(3 downto 0);
  signal y2 : std_logic_vector(3 downto 0);
  signal y4 : std_logic_vector(3 downto 0);
  signal y9 : std_logic_vector(3 downto 0);
begin
  u0 : entity work.case_generate
    generic map (OP => 0)
    port map (a => a, b => b, y => y0);
  u2 : entity work.case_generate
    generic map (OP => 2)
    port map (a => a, b => b, y => y2);
  u4 : entity work.case_generate
    generic map (OP => 4)
    port map (a => a, b => b, y => y4);
  u9 : entity work.case_generate
    generic map (OP => 9)
    port map (a => a, b => b, y => y9);

  check : process
  begin
    for k in as'range loop
      a <= as(k);
      b <= bs(k);
      wait for 10 ns;
      assert y0 = (as(k) and bs(k)) report "OP 0: and" severity failure;
      assert y2 = (as(k) or bs(k)) report "OP 2: or" severity failure;
      assert y4 = (as(k) xor bs(k)) report "OP 4: xor" severity failure;
      assert y9 = not as(k) report "OP 9: not" severity failure;
    end loop;
    std.env.finish;
    wait;
  end process check;
end architecture sim;
