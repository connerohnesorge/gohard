-- Mode views used correctly: a view, its converse, a subview and an
-- array of subviews, ports passed through to instances. NVC 1.23
-- analyzes it cleanly; its elaboration then reports "multiple sources"
-- for lanes(i).valid, which only src drives (an NVC limitation with
-- array subviews; without lanes it elaborates).
package bus_pkg is
  type lane_t is record
    valid : bit;
    ready : bit;
  end record lane_t;
  type lanes_t is array (0 to 1) of lane_t;
  type bus_t is record
    ctl   : lane_t;
    lanes : lanes_t;
    data  : bit_vector(7 downto 0);
  end record bus_t;

  view lane_src_v of lane_t is
    valid : out;
    ready : in;
  end view lane_src_v;

  view src_v of bus_t is
    ctl   : view lane_src_v;
    lanes : view (lane_src_v);
    data  : out;
  end view src_v;

  alias sink_v is src_v'converse;
end package bus_pkg;
