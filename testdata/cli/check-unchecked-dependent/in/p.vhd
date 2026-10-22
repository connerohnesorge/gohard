package p is
  type t is protected
    generic (n : natural);
    procedure inc;
  end protected t;
  constant k : integer := 1;
end package p;
