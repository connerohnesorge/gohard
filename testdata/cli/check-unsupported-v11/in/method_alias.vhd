package method_alias is
  type t is protected
    procedure inc;
    alias bump is inc [];
  end protected t;
end package method_alias;
