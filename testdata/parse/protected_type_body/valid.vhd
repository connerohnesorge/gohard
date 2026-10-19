package body p is
  type counter is protected body
    variable n : natural := 0;
    procedure inc is
    begin
      n := n + 1;
    end procedure;
  end protected body counter;
end;
