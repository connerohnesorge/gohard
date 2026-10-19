package bus_pkg is
  type bus_t is record
    req : bit;
    ack : bit;
  end record bus_t;
  view producer_v of bus_t is
    req : out;
    ack : in;
  end view producer_v;
end package bus_pkg;

use work.bus_pkg.all;
entity producer is
  port (b : view producer_v);
end entity producer;

architecture rtl of producer is
  procedure drive(signal o : out bit) is
  begin
    o <= '1';
  end procedure drive;
  procedure toggle(signal o : inout bit) is
  begin
    o <= not o;
  end procedure toggle;
begin
  process is
  begin
    drive(b.req);
    drive(b.ack);
    toggle(b.ack);
    wait;
  end process;
end architecture rtl;
