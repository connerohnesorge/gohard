-- 'PATH_NAME and 'INSTANCE_NAME inside protected types (VHDL-2019,
-- LRM 16.2.5): a method, its variables and parameters and the type's
-- variables are named under the protected object's own path, that of
-- a shared variable, of a private variable of another object, of a
-- process variable, or of an allocated object (the empty name in the
-- allocating process, NVC's _p0 for an unlabeled one).
entity names_prot_tb is
end entity names_prot_tb;

architecture y of names_prot_tb is
  procedure check(tag, got, want : string) is
  begin
    report tag & " " & got;
    assert got = want report "wrong " & tag & ": " & got severity error;
  end procedure check;

  type inner is protected
    impure function hi return string;
  end protected inner;

  type inner is protected body
    variable z : integer;

    impure function hi return string is
    begin
      return hi'path_name & " " & z'path_name & " " & z'instance_name;
    end function hi;
  end protected body inner;

  type outer is protected
    impure function go return string;
    impure function name return string;
  end protected outer;

  type outer is protected body
    variable in1 : inner;

    impure function go return string is
    begin
      return in1.hi;
    end function go;

    impure function name return string is
    begin
      return name'path_name & " " & in1'instance_name;
    end function name;
  end protected body outer;

  type pt is protected
    procedure m(a : integer; b : bit);
  end protected pt;

  type pt is protected body
    variable iv : integer;

    procedure m(a : integer; b : bit) is
      variable lv : integer;
    begin
      report "m " & m'path_name & " " & m'instance_name & " " &
        lv'path_name & " " & iv'path_name & " " & a'instance_name;
    end procedure m;
  end protected body pt;

  shared variable so : outer;
  shared variable sv : pt;

  type acc is access inner;
begin
  process
    variable p : acc;
    variable pv : pt;
  begin
    check("go", so.go, ":names_prot_tb:so:in1:hi[return string]: " &
      ":names_prot_tb:so:in1:z :names_prot_tb(y):so:in1:z");
    check("name", so.name, ":names_prot_tb:so:name[return string]: " &
      ":names_prot_tb(y):so:in1");
    sv.m(1, '0');
    pv.m(1, '0');
    p := new inner;
    check("new", p.all.hi, ":names_prot_tb:_p0::hi[return string]: " &
      ":names_prot_tb:_p0::z :names_prot_tb(y):_p0::z");
    wait;
  end process;
end architecture y;
