package p is
  function id generic (type t) parameter (x : t) return t;
  procedure swap generic (type t) (a, b : inout t);
end;
