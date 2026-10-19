entity e is
  generic (
    package p1 is new work.gp generic map (<>);
    package p2 is new work.gp generic map (default);
    package p3 is new work.gp generic map (n => 4)
  );
end;
