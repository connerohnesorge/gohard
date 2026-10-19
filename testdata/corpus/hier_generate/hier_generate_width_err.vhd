-- hier_generate_width_err: error variant of hier_generate.vhd (#8).
-- Analyzed in place of hier_generate.vhd. Identical except that lane 2
-- alone is connected through a slice one bit narrower than its port
-- (W = 4, actual is 3 bits), so exactly one generate instance,
-- lanes(2).u, has a port width mismatch. The other lanes are unchanged.
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
    -- The defect: lane 2 loses its top bit.
    constant TOP : natural := HI - boolean'pos(i = 2);
  begin
    u : entity work.hier_generate_lane
      generic map (W => BASE + i, INVERT => i mod 2 = 1)
      port map (a => din(TOP downto LO), y => dout(TOP downto LO));
  end generate lanes;

  tap : if multi : N > 1 generate
    parity <= xor din;
  else single : generate
    parity <= '0';
  end generate tap;
end architecture rtl;
