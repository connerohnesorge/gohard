-- Constants of packages and architectures initialised from std.env
-- natives and impure functions are elaborated once per instance, with
-- the declaration's line for FILE_LINE (LRM 14.4.2.1, 16.5).
use std.env.all;

package design_constants_pkg is
  constant PKG_VERSION : string := VHDL_VERSION;
  constant PKG_LINE    : positive := FILE_LINE;
end package design_constants_pkg;

use std.env.all;

entity design_constants_sub is
  generic (step : integer);
end entity design_constants_sub;

architecture sim of design_constants_sub is
  type counter_t is protected
    impure function bump(k : integer) return integer;
  end protected counter_t;

  type counter_t is protected body
    variable count : integer := 0;
    impure function bump(k : integer) return integer is
    begin
      count := count + k;
      return count;
    end function bump;
  end protected body counter_t;

  shared variable counter : counter_t;

  impure function bump return integer is
  begin
    return counter.bump(step);
  end function bump;

  constant FIRST  : integer := bump;
  constant SECOND : integer := bump + FIRST;
  constant TOOL   : string  := TOOL_NAME;
  constant UNSET  : string  := GETENV("GOHARD_CONFORMANCE_UNSET");
begin
  process
  begin
    assert TOOL'length > 0 report "empty TOOL_NAME" severity error;
    report "step " & integer'image(step) & ": " & integer'image(FIRST) &
      " " & integer'image(SECOND) & " [" & UNSET & "]";
    wait;
  end process;
end architecture sim;

use work.design_constants_pkg.all;
use std.env.all;

entity design_constants_tb is
end entity design_constants_tb;

architecture sim of design_constants_tb is
  constant ARCH_LINE : positive := FILE_LINE;
  constant ARCH_TEXT : string :=
    PKG_VERSION & " " & integer'image(PKG_LINE) & " " &
    integer'image(ARCH_LINE);
begin
  u1 : entity work.design_constants_sub generic map (step => 1);
  u2 : entity work.design_constants_sub generic map (step => 10);

  process
  begin
    report ARCH_TEXT;
    wait;
  end process;
end architecture sim;
