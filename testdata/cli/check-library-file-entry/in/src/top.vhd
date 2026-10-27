library special, biglib;
use special.p;
use biglib.q;

entity top is end;

architecture rtl of top is
  constant x : integer := p.k + q.k;
begin
end;
