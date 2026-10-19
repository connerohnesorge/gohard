use work.bus_pkg.all;

entity consumer is
  port (b : view consumer_v);
end entity consumer;

architecture rtl of consumer is
begin
  b.ack <= '1';
  -- Misuse: req has mode in in the converse view.
  b.req <= '0';
end architecture rtl;

use work.bus_pkg.all;

entity producer is
  port (b : view producer_v; done : in bit);
end entity producer;

architecture rtl of producer is
begin
  b.req  <= done;
  b.data <= (others => '0');
  -- Misuse: the whole port has an element of mode in (ack).
  b <= ('0', x"00", '0');
end architecture rtl;

use work.bus_pkg.all;

entity top is
  port (link_in : in bus_t);
end entity top;

architecture rtl of top is
  signal link : bus_t;
begin
  u_prod : entity work.producer port map (b => link, done => '0');
  u_cons : entity work.consumer port map (b => link);
  -- Misuse: producer_v drives req and data, but link_in is mode in.
  u_bad : entity work.producer port map (b => link_in, done => '0');
end architecture rtl;
