package protected_generics is
  type t is protected
    generic (width : natural);
    procedure inc;
  end protected t;
end package protected_generics;
