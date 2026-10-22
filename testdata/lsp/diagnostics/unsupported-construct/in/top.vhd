entity leaf is
end entity leaf;

architecture rtl of leaf is
begin
end architecture rtl;

entity top is
end entity top;

architecture rtl of top is
  component leaf is
  end component leaf;
begin
  u1 : component leaf;
end architecture rtl;

configuration top_cfg of top is
  for rtl
    for u1 : leaf
      use entity work.leaf(rtl);
      use vunit checks;
    end for;
  end for;
end configuration top_cfg;
