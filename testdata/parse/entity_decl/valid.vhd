entity e is
  generic (n : natural := 4);
  port (a : in bit; b : out bit);
  constant k : natural := n;
begin
  assert n > 0 report "n" severity failure;
end entity e;
