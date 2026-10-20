entity sub is
  generic (WIDTH : natural := 4);
  port (din : in bit; dout : out bit);
end entity;
architecture rtl of sub is
  signal tmp : bit;
  signal other : bit;
begin
  tmp <= din;
  other <= tmp;
  dout <= other;
end architecture;
