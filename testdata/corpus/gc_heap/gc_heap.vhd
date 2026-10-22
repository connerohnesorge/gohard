-- gc_heap: design under test for the gc_heap corpus design (#27).
-- A queue of integers held in a linked list of access-type nodes.
-- A pop drops the head node without DEALLOCATE: under VHDL-2019 the
-- simulator's garbage collector reclaims it.
library ieee;
use ieee.std_logic_1164.all;

entity gc_heap is
  port (
    clk   : in  std_logic;
    push  : in  std_logic;
    pop   : in  std_logic;
    din   : in  integer;
    dout  : out integer;
    count : out natural
  );
end entity gc_heap;

architecture behav of gc_heap is
begin
  queue : process (clk)
    type node;
    type node_ptr is access node;
    type node is record
      value : integer;
      link  : node_ptr;
    end record;
    variable head, tail : node_ptr;
    variable n : natural := 0;
  begin
    if rising_edge(clk) then
      if pop = '1' and head /= null then
        dout <= head.value;
        head := head.link; -- the old head is garbage now
        if head = null then
          tail := null;
        end if;
        n := n - 1;
      end if;
      if push = '1' then
        if tail = null then
          tail := new node'(din, null);
          head := tail;
        else
          tail.link := new node'(din, null);
          tail := tail.link;
        end if;
        n := n + 1;
      end if;
      count <= n;
    end if;
  end process queue;
end architecture behav;
