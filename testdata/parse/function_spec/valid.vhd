package p is
  pure function f1 return bit;
  impure function f2 (a : bit) return bit;
  function "+" (a, b : t) return t;
end;
