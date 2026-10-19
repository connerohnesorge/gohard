package call_generic_map is
  function scale
    generic (k : integer)
    parameter (x : integer) return integer;
end package call_generic_map;

package body call_generic_map is
  function scale
    generic (k : integer)
    parameter (x : integer) return integer is
  begin
    return k * x;
  end function scale;

  function twice (x : integer) return integer is
  begin
    return scale generic map (k => 2) (x => x);
  end function twice;
end package body call_generic_map;
