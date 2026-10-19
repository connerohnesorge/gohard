-- hier_generate: generics plus for/if generate (#8).
-- N lanes; lane i is W = BASE + i bits wide and sits at bit offset
-- i*BASE + i*(i-1)/2 of the packed din/dout buses. Odd lanes invert.
-- An outer if-generate adds a parity tap only when N > 1.
-- Instance paths: hier_generate_tb.dut.lanes(i).u.polarity.{inv,pass}.
library ieee;
use ieee.std_logic_1164.all;

entity hier_generate is
  generic (
    N    : positive := 4;
    BASE : positive := 2
  );
  port (
    din    : in  std_logic_vector(N * BASE + N * (N - 1) / 2 - 1 downto 0);
    dout   : out std_logic_vector(N * BASE + N * (N - 1) / 2 - 1 downto 0);
    parity : out std_logic
  );
end entity hier_generate;

architecture rtl of hier_generate is
begin
  lanes : for i in 0 to N - 1 generate
    constant LO : natural := i * BASE + i * (i - 1) / 2;
    constant HI : natural := LO + BASE + i - 1;
  begin
    u : entity work.hier_generate_lane
      generic map (W => BASE + i, INVERT => i mod 2 = 1)
      port map (a => din(HI downto LO), y => dout(HI downto LO));
  end generate lanes;

  tap : if multi : N > 1 generate
    parity <= xor din;
  else single : generate
    parity <= '0';
  end generate tap;
end architecture rtl;
