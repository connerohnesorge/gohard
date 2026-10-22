-- The methods of an object of a generic protected type take the
-- object's generic type actuals in their profiles (LRM 5.6.2,
-- 6.4.2.4): a parameterless method's result is indexed, compared with
-- a literal, concatenated and given to an operator, and a literal or
-- aggregate actual of a parameter of the generic type has the actual
-- type, for a variable's generic map, an instantiated type and an
-- object reached through an access value. NVC 1.23 does not parse
-- generic protected types, so there is no NVC recording.
entity method_profiles_tb is
end entity method_profiles_tb;

architecture sim of method_profiles_tb is
  type box is protected
    generic (type t; d : t);
    procedure set(x : t);
    impure function get return t;
    impure function dflt return t;
  end protected box;

  type box is protected body
    variable v : t := d;
    procedure set(x : t) is
    begin
      v := x;
    end procedure set;
    impure function get return t is
    begin
      return v;
    end function get;
    impure function dflt return t is
    begin
      return d;
    end function dflt;
  end protected body box;

  type iv is array (1 to 3) of integer;
  type ibox is new box generic map (t => iv, d => (7, 8, 9));
  type ibox_ptr is access ibox;
begin
  process
    variable a : ibox;
    variable s : box generic map (t => string(1 to 3), d => "abc");
    variable b : box generic map (t => bit, d => '1');
    variable n : box generic map (t => integer, d => 5);
    variable p : ibox_ptr := new ibox;
  begin
    report "a " & integer'image(a.get(1)) & " " &
      integer'image(a.get(3) + a.dflt(2));
    a.set((1, 2, 3));
    assert a.get = (1, 2, 3) severity failure;
    assert a.get(2) = 2 severity failure;
    report "s " & s.get & "!" & s.get(2);
    s.set("xyz");
    assert s.get = "xyz" severity failure;
    if b.get = '1' then
      report "b one";
    end if;
    b.set('0');
    report "b " & bit'image(b.get and b.dflt);
    n.set(40);
    report "n " & integer'image(n.get + 2) & " " &
      integer'image(-n.get);
    p.all.set((n.get, n.dflt, a.get(3)));
    report "p " & integer'image(p.all.get(1) + p.all.get(2) +
      p.all.get(3));
    wait;
  end process;
end architecture sim;
