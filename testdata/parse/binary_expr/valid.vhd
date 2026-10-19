package p is
  constant c : integer := a + b * c ** 2 - d mod 3 rem 2;
  constant d : boolean := ((a = b) and (c /= d)) or (e xor f);
  constant e : bit_vector := v sll 2 & w;
  constant f : boolean := a ?= b;
end;
