entity e is
  generic (
    type t;
    function less (a, b : t) return boolean;
    procedure show (x : t)
  );
end;
