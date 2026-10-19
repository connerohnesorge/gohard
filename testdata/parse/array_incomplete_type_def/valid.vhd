entity e is
  generic (
    type el is private;
    type arr is array (natural range <>) of el
  );
end;
