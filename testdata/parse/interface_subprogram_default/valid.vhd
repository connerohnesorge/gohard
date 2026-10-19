entity e is
  generic (
    type t;
    function eq (a, b : t) return boolean is <>;
    function hash (a : t) return natural is default_hash
  );
end;
