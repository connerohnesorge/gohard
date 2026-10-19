-- A record bus, its producer view and the converse for consumers.
package bus_pkg is
  type bus_t is record
    req  : bit;
    data : bit_vector(7 downto 0);
    ack  : bit;
  end record bus_t;

  view producer_v of bus_t is
    req  : out;
    data : out;
    ack  : in;
  end view producer_v;

  alias consumer_v is producer_v'converse;

  -- Misuse: ack is given no mode, req is given two.
  view broken_v of bus_t is
    req  : out;
    data : out;
    req  : in;
  end view broken_v;
end package bus_pkg;
