entity e is
  generic (
    type t;
    function eq (a, b : t) return boolean is <>;
    package g is new work.gp generic map (n => <>, m => 1)
  );
end;
