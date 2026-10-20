library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
library util;
use util.util_pkg.all;

entity top is
end entity top;

architecture rtl of top is
  component sub is
    generic (DEPTH : natural := 4);
    port (a : in std_logic; y : out std_logic);
  end component sub;
  -- The request line.
  signal request, ack : std_logic;
  signal st       : state_t := idle;
  signal xf       : util.util_pkg.xfer_t;
  signal cnt      : unsigned(3 downto 0);
  signal \My Signal\ : std_logic;
begin
  u1 : entity util.sub
    generic map (DEPTH => 2)
    port map (a => request, y => ack);
  u2 : sub
    generic map (DEPTH => BUS_W)
    port map (a => request, y => xf.data(0));
  \My Signal\ <= REQUEST;
  p : process
    variable v : integer;
  begin
    v   := INC(util.util_pkg.WIDTH) + xf.addr;
    cnt <= cnt + 1;
    st  <= busy;
    lp : for i in 0 to 3 loop
      exit lp when i = to_integer(cnt);
    end loop lp;
    wait;
  end process p;
end architecture rtl;
