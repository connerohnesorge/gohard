entity top is
  port(a : in bit; b : out bit);
end entity;

architecture rtl of top is
begin
  b <= a;
end;
