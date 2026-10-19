package p is
  type bus_t is record
    req  : bit;
    ack  : bit;
  end record;
  view master of bus_t is
    req : out;
    ack : in;
  end view master;
  alias slave is master'converse;
end;
