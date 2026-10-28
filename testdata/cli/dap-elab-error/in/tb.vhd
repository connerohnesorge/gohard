-- An impure function called by a signal's initial value: impure
-- functions run in elaboration order against the design's shared
-- state, which GoHard's elaboration does not model yet, so the run
-- stops at elaboration with unsupported/construct (please report)
-- instead of a sim/runtime-error.
entity tb is
end entity tb;

architecture sim of tb is
  type int_ptr is access integer;

  impure function seven return integer is
    variable p : int_ptr := new integer'(7);
  begin
    return p.all;
  end function seven;

  signal s : integer := seven;
begin
  process
  begin
    report "s = " & integer'image(s);
    wait;
  end process;
end architecture sim;
