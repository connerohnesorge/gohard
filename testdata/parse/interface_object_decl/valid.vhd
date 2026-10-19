entity e is
  generic (constant n : integer := 1);
  port (signal a, b : in bit := '0'; c : inout bit bus; l : linkage bit);
end;
