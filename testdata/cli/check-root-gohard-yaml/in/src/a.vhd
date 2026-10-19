library mylib;
use mylib.p.all;

entity a is
end entity a;

architecture rtl of a is
  constant c : integer := k + mylib.p.k;
begin
end architecture rtl;
