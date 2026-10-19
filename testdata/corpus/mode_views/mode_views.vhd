-- mode_views: producer and consumer connected through one record signal.
-- The producer port uses producer_v, the consumer port its 'converse.
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
use work.mode_views_pkg.all;

entity mv_producer is
  port (
    clk : in std_logic;
    b   : view producer_v
  );
end entity mv_producer;

architecture rtl of mv_producer is
begin
  -- Four-phase handshake: send 1, 2, 3, 4 (each xor C_XOR_KEY).
  process
  begin
    b.req  <= '0';
    b.data <= (others => '0');
    for i in 1 to 4 loop
      wait until rising_edge(clk);
      b.data <= std_logic_vector(to_unsigned(i, 8)) xor C_XOR_KEY;
      b.req  <= '1';
      wait until rising_edge(clk) and b.ack = '1';
      b.req  <= '0';
      wait until rising_edge(clk) and b.ack = '0';
    end loop;
    wait;
  end process;
end architecture rtl;

library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
use work.mode_views_pkg.all;

entity mv_consumer is
  port (
    clk   : in  std_logic;
    b     : view consumer_v;
    sum   : out natural;
    count : out natural
  );
end entity mv_consumer;

architecture rtl of mv_consumer is
begin
  process
    variable s : natural := 0;
    variable n : natural := 0;
  begin
    b.ack <= '0';
    sum   <= 0;
    count <= 0;
    loop
      wait until rising_edge(clk) and b.req = '1';
      s := s + to_integer(unsigned(b.data xor C_XOR_KEY));
      n := n + 1;
      sum   <= s;
      count <= n;
      b.ack <= '1';
      wait until rising_edge(clk) and b.req = '0';
      b.ack <= '0';
    end loop;
  end process;
end architecture rtl;

library ieee;
use ieee.std_logic_1164.all;
use work.mode_views_pkg.all;

entity mode_views is
  port (
    clk   : in  std_logic;
    sum   : out natural;
    count : out natural
  );
end entity mode_views;

architecture rtl of mode_views is
  signal link : bus_t;
begin
  u_prod : entity work.mv_producer
    port map (clk => clk, b => link);
  u_cons : entity work.mv_consumer
    port map (clk => clk, b => link, sum => sum, count => count);
end architecture rtl;
