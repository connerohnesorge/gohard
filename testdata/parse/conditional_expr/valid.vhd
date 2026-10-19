package p is
  constant c : integer := 1 when sel else 2;
  constant d : integer := 1 when a else 2 when b else 3;
end;
