entity  top IS
port(a:in bit;
b:out bit);
END;
architecture rtl of top is
begin
      b<=a;
end;
