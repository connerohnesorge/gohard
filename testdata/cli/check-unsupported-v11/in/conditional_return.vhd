package conditional_return is
  function clip (x : integer) return integer;
end package conditional_return;

package body conditional_return is
  function clip (x : integer) return integer is
  begin
    return 0 when x < 0;
    return x;
  end function clip;
end package body conditional_return;
