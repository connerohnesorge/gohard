package p is
  type counter is protected
    procedure inc;
    impure function get return natural;
  end protected counter;
end;
