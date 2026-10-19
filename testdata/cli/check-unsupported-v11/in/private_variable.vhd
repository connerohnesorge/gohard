package private_variable is
  type t is protected
    private variable n : integer;
    procedure inc;
  end protected t;
end package private_variable;
