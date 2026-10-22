-- Garbage collection and std.reflection (VHDL-2019, #27): a value
-- mirror of a list keeps the nodes it reflects after the list's
-- variable is set to null and more objects are allocated than one
-- collection period, and a live mirror gives the same subtype mirror
-- again. Mirrors made and dropped in a loop are collected like any
-- other access value.
-- nvc-differs: NVC 1.23 stops with "unsupported type NODE in prefix of REFLECT attribute" (no mirror of an access to a record)
use std.reflection.all;

entity dropped_mirrors_tb is
end entity dropped_mirrors_tb;

architecture sim of dropped_mirrors_tb is
  type node;
  type link is access node;
  type node is record
    v   : integer;
    nxt : link;
  end record node;
begin
  check : process
    variable vm, tmp : value_mirror;
    variable s1, s2 : subtype_mirror;
    variable l, t : link;
    variable s, second : integer := 0;
  begin
    t := new node'(2, null);
    l := new node'(1, t);
    t := null;
    vm := l'reflect;
    s1 := vm.get_subtype_mirror;
    l := null;
    for i in 1 to 10000 loop
      t := new node'(i, null);
      tmp := t'reflect;
      s := s + tmp.to_access.get.to_record.get(0).to_integer.value;
    end loop;
    s2 := vm.get_subtype_mirror;
    second := vm.to_access.get.to_record.get("nxt").to_access.get.
      to_record.get(0).to_integer.value;
    report "second node " & integer'image(second);
    assert second = 2 severity failure;
    report "sum " & integer'image(s);
    assert s = 50005000 severity failure;
    assert s1 = s2 report "the subtype mirror changed" severity failure;
    wait;
  end process check;
end architecture sim;
