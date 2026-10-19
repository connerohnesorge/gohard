entity d is
  port(a : in bit; b : out bit);
end entity;

architecture rtl of d is
begin
  b <= a;
end;
   
