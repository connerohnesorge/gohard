-- Garbage collection of access-type objects (VHDL-2019, #27): a
-- process builds a 300-node linked list 30 times and drops it each
-- time without DEALLOCATE, a procedure-local LINE is dropped per round,
-- and an access to a protected object is dropped per round. Nothing
-- reachable is ever lost: every list is walked and summed after the
-- collections the earlier rounds triggered, and a list kept from an
-- early round is still intact at the end.
use std.textio.all;

entity dropped_lists_tb is
end entity dropped_lists_tb;

architecture sim of dropped_lists_tb is
  type node;
  type node_ptr is access node;
  type node is record
    value : integer;
    link  : node_ptr;
  end record;

  type tally is protected
    procedure add(x : integer);
    impure function total return integer;
  end protected tally;

  type tally is protected body
    variable sum : integer := 0;
    procedure add(x : integer) is
    begin
      sum := sum + x;
    end procedure add;
    impure function total return integer is
    begin
      return sum;
    end function total;
  end protected body tally;

  type tally_ptr is access tally;

  procedure note(x : integer) is
    variable l : line;
  begin
    write(l, x); -- dropped when the procedure returns
  end procedure note;
begin
  check : process
    variable head, kept, p : node_ptr;
    variable t : tally_ptr;
    variable sum : integer;
  begin
    for round in 1 to 30 loop
      head := null;
      for i in 1 to 300 loop
        head := new node'(i + round, head);
      end loop;
      t := new tally;
      p := head;
      while p /= null loop
        t.add(p.value);
        p := p.link;
      end loop;
      assert t.total = 45150 + 300 * round
        report "round " & integer'image(round) & ": sum " &
          integer'image(t.total);
      note(t.total);
      if round = 3 then
        kept := head;
      end if;
      wait for 1 ns;
    end loop;
    sum := 0;
    p := kept;
    while p /= null loop
      sum := sum + p.value;
      p := p.link;
    end loop;
    report "30 lists of 300 nodes summed; the list kept from round 3 " &
      "sums to " & integer'image(sum);
    wait;
  end process check;
end architecture sim;
