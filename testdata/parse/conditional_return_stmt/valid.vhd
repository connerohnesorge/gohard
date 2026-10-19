package body p is
  function f (x : integer) return integer is
  begin
    return 1 when x > 0 else 0;
  end function;
  procedure q (x : integer) is
  begin
    return when x = 0;
  end procedure;
end;
