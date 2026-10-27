use work.units_si.all;
use work.units_us.all;

entity top is
end entity top;

architecture rtl of top is
  -- m is a literal of both length types: "=" cannot tell them apart.
  constant same : boolean := m = m;
  -- Both scale functions take and return integer.
  constant big : integer := scale(3);
  -- Fine: the expanded name selects one.
  constant si : integer := work.units_si.scale(3);
begin
  process
  begin
    log(1);
    wait;
  end process;
end architecture rtl;
