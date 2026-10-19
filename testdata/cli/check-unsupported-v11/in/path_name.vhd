package path_name is
  type t is protected
    procedure show;
  end protected t;
end package path_name;

package body path_name is
  type t is protected body
    variable n : integer := 0;
    procedure show is
    begin
      report n'path_name;
    end procedure show;
  end protected body t;
end package body path_name;
