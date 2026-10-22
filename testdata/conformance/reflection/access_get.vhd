-- nvc-differs: NVC 1.23 mirrors the access value, not the object
-- GET of an access value mirror is the VALUE_MIRROR of the designated
-- object, as it was when the mirror was made; a list is followed.
use std.reflection.all;

entity access_get_tb is
end entity access_get_tb;

architecture sim of access_get_tb is
  type iptr is access integer;
  type node;
  type link is access node;
  type node is record
    v   : integer;
    nxt : link;
  end record node;
begin
  check : process
    variable p  : iptr;
    variable vm : value_mirror;
    variable l  : link;
  begin
    p := new integer'(42);
    vm := p'reflect;
    p.all := 43;
    report "then " & vm.to_access.get.to_integer.image & ", now " &
      p'reflect.to_access.get.to_integer.image;
    l := new node'(1, new node'(2, null));
    report "second node " & l'reflect.to_access.get.to_record.get("nxt").
      to_access.get.to_record.get(0).to_integer.image;
    wait;
  end process check;
end architecture sim;
