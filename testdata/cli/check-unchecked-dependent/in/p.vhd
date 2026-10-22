package p is
  type t is protected
    procedure inc;
    alias bump is inc [];
  end protected t;
  constant k : integer := 1;
end package p;
