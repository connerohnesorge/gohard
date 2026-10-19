package p is
  type pt is protected
    private variable count : natural;
    procedure inc;
  end protected pt;
end;
