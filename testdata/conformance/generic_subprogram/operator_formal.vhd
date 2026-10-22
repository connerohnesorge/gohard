-- A generic subprogram whose designator is an operator symbol is named
-- as a formal in a generic map (LRM 6.5.6.2, 6.5.7.1): ">" => lt in a
-- VHDL-2008 style instance and in a VHDL-2019 call with a generic map;
-- without an association the is <> default, the predefined ">", is
-- used.
entity operator_formal_tb is
end entity operator_formal_tb;

architecture sim of operator_formal_tb is
  function mx
    generic (type t; function ">" (a, b : t) return boolean is <>)
    parameter (a, b : t) return t is
  begin
    if a > b then
      return a;
    end if;
    return b;
  end function mx;

  function lt (a, b : integer) return boolean is
  begin
    return a < b;
  end function lt;

  function imn is new mx generic map (t => integer, ">" => lt);
  function imx is new mx generic map (t => integer);
begin
  check : process
    variable v : integer;
  begin
    v := imn(3, 9);
    report "imn = " & integer'image(v);
    assert v = 3 severity failure;
    v := imx(3, 9);
    report "imx = " & integer'image(v);
    assert v = 9 severity failure;
    v := mx generic map (t => integer, ">" => lt) (3, 9);
    report "call min = " & integer'image(v);
    assert v = 3 severity failure;
    v := mx generic map (t => integer) (3, 9);
    report "call max = " & integer'image(v);
    assert v = 9 severity failure;
    wait;
  end process check;
end architecture sim;
