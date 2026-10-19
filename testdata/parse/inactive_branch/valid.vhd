-- test: define=A=1
`if A = "2" then
package never_p is
  constant c : integer := 2;
end;
`end if
package p is
end;
