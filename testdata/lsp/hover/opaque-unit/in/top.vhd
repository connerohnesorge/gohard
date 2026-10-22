entity leaf is
end entity leaf;

architecture rtl of leaf is
begin
end architecture rtl;

use work.counter.all;

entity top is
end entity top;

architecture rtl of top is
  shared variable c : counter_t;
  component leaf is
  end component leaf;
  for u1 : leaf
    use entity work.leaf(rtl);
    use vunit checks;
  end for;
begin
  u1 : component leaf;
end architecture rtl;
