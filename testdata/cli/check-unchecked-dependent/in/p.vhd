package p is
  type t is protected
    private variable n : integer;
    procedure inc;
  end protected t;
  constant k : integer := 1;
end package p;
