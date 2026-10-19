package p is
  type bus_t is record
    req : bit;
  end record;
  view master bus_t is
    req : out;
  end view;
end;
