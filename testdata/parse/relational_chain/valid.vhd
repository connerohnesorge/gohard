-- test: std=2008 kind=BinaryExpr
package p is
  constant c : boolean := (a = b) = c;
  constant d : boolean := a < b and b /= c;
end;
