-- prot_cond: package for the VHDL-2019 v1.1 features (#18).
-- Protected type, conditional return, a generic subprogram and
-- conditional analysis on a built-in identifier.
package prot_cond_pkg is

  type counter_t is protected
    procedure inc;
    impure function get return integer;
  end protected counter_t;

  -- Conditional `return ... when`.
  function clip(x : integer) return integer;

  -- Uninstantiated subprogram, called with a generic map.
  function scale
    generic (k : integer)
    parameter (x : integer) return integer;

  -- Built-in identifier; must equal std.env.TOOL_NAME.
`if TOOL_NAME = "gohard" then
  constant C_TOOL : string := "gohard";
`elsif TOOL_NAME = "nvc" then
  constant C_TOOL : string := "nvc";
`else
  constant C_TOOL : string := "unknown";
`end if

end package prot_cond_pkg;

package body prot_cond_pkg is

  type counter_t is protected body
    variable n : integer := 0;

    procedure inc is
    begin
      n := n + 1;
    end procedure inc;

    impure function get return integer is
    begin
      return n;
    end function get;
  end protected body counter_t;

  function clip(x : integer) return integer is
  begin
    return 0 when x < 0;
    return x;
  end function clip;

  function scale
    generic (k : integer)
    parameter (x : integer) return integer is
  begin
    return k * x;
  end function scale;

end package body prot_cond_pkg;
