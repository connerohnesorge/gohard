-- Generic subprograms of subprogram instances (LRM 6.5.6.2): a call
-- of the formal calls its actual, named in the generic map, a
-- predefined operator, or the default (is <>) visible at the instance.
entity generic_subprogram_tb is
end entity generic_subprogram_tb;

architecture sim of generic_subprogram_tb is
  function pick
    generic (type t; function better (a, b : t) return boolean)
    parameter (a, b : t) return t is
  begin
    if better(a, b) then
      return a;
    end if;
    return b;
  end function pick;

  function largest
    generic (type t; function "<" (a, b : t) return boolean is <>)
    parameter (a, b, c : t) return t is
    variable m : t := a;
  begin
    if m < b then
      m := b;
    end if;
    if m < c then
      m := c;
    end if;
    return m;
  end function largest;

  procedure apply
    generic (type t; procedure step (variable x : inout t))
    parameter (variable x : inout t; n : natural) is
  begin
    for i in 1 to n loop
      step(x);
    end loop;
  end procedure apply;

  function closer (a, b : integer) return boolean is
  begin
    return abs a < abs b;
  end function closer;

  procedure twice (variable x : inout integer) is
  begin
    x := 2 * x;
  end procedure twice;

  function max_int is new pick generic map (t => integer, better => ">");
  function nearest is new pick generic map (t => integer,
    better => closer);
begin
  p : process
    variable v : integer := 3;
  begin
    report "max_int(3, 9) = " & integer'image(max_int(3, 9));
    report "nearest(-7, 4) = " & integer'image(nearest(-7, 4));
    report "min(2.5, 1.5) = " & real'image(
      pick generic map (t => real, better => "<") (2.5, 1.5));
    report "largest(4, 11, 6) = " &
      integer'image(largest generic map (t => integer) (4, 11, 6));
    report "largest('q', 'z', 'a') = " &
      character'image(largest generic map (t => character) ('q', 'z', 'a'));
    apply generic map (t => integer, step => twice) parameter map (v, 3);
    report "apply twice 3 times = " & integer'image(v);
    assert max_int(3, 9) = 9 and nearest(-7, 4) = 4 and v = 24
      report "generic subprograms" severity failure;
    wait;
  end process p;
end architecture sim;
