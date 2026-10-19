package p is
  view outer of outer_t is
    plain : inout;
    nested : view inner;
    arr : view (inner);
  end view;
end;
