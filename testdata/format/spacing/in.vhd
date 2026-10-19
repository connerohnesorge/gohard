entity sp is
  port(a,b:in integer;y   :   out integer;z:out boolean);
end;
architecture a of sp is
  signal s:integer:=0;
  type arr is array(0 to 3)of integer;
  constant k:arr:=(0=>1,1=>2,others=>0);
begin
  y<=a+b*2-(a/b)mod 3 rem 2;
  z<=(a>=b and a/=b)or not(a<b);
  s<=-a**2+abs a;
  process(a)is
    variable v:integer;
  begin
    v:=k(a)+k ( b );
    report"x"&integer'image(v)severity note;
  end process;
end;
