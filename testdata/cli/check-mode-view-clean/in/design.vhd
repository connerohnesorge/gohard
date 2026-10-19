use work.bus_pkg.all;

entity src is
  port (b : view src_v);
end entity src;

architecture rtl of src is
begin
  b.ctl.valid      <= b.ctl.ready;
  b.lanes(0).valid <= '1';
  b.lanes(1).valid <= b.lanes(1).ready;
  b.data           <= (others => '1');
end architecture rtl;

use work.bus_pkg.all;

entity sink is
  port (b : view sink_v);
end entity sink;

architecture rtl of sink is
begin
  b.ctl.ready <= b.ctl.valid;
  lanes : for i in 0 to 1 generate
    b.lanes(i).ready <= b.lanes(i).valid;
  end generate lanes;
end architecture rtl;

use work.bus_pkg.all;

-- A wrapper passing its view port straight through.
entity src_wrap is
  port (b : view src_v);
end entity src_wrap;

architecture rtl of src_wrap is
begin
  u : entity work.src port map (b => b);
end architecture rtl;

use work.bus_pkg.all;

entity top is
end entity top;

architecture rtl of top is
  signal link : bus_t;
begin
  u_src  : entity work.src_wrap port map (b => link);
  u_sink : entity work.sink port map (b => link);
end architecture rtl;
