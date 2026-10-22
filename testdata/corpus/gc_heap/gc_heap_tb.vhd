-- gc_heap_tb: self-checking testbench of the access heap (#27).
-- Streams 200 values through the linked-list queue of gc_heap and
-- checks their order, then builds and drops a 500-node list 40 times
-- (20,000 allocations, none deallocated) and checks every list's sum.
-- Garbage collection is not visible in VHDL: the design passes, with
-- the same trace, whether or not the dropped nodes are reclaimed; the
-- simulator's heap statistics show that they are (live objects stay
-- bounded). It ends with std.env.finish.
library ieee;
use ieee.std_logic_1164.all;
use std.textio.all;

entity gc_heap_tb is
end entity gc_heap_tb;

architecture sim of gc_heap_tb is
  signal clk   : std_logic := '0';
  signal push  : std_logic := '0';
  signal pop   : std_logic := '0';
  signal din   : integer := 0;
  signal dout  : integer;
  signal count : natural;
  signal done  : boolean := false;

  type cell;
  type cell_ptr is access cell;
  type cell is record
    value : integer;
    link  : cell_ptr;
  end record;

  -- A new list of n cells holding 1 .. n, last first.
  impure function build(n : natural) return cell_ptr is
    variable p : cell_ptr;
  begin
    for i in 1 to n loop
      p := new cell'(i, p);
    end loop;
    return p;
  end function build;

  impure function sum(variable p : in cell_ptr) return integer is
    variable q : cell_ptr := p;
    variable s : integer := 0;
  begin
    while q /= null loop
      s := s + q.value;
      q := q.link;
    end loop;
    return s;
  end function sum;
begin
  dut : entity work.gc_heap
    port map (clk => clk, push => push, pop => pop, din => din,
              dout => dout, count => count);

  clk <= not clk after 5 ns when not done;

  stream : process
    variable expect : integer := 0;
  begin
    for i in 1 to 200 loop
      wait until falling_edge(clk);
      push <= '1';
      din <= i * 7;
      pop <= '1' when i > 3 else '0';
      if i > 4 then
        expect := expect + 1;
        assert dout = expect * 7
          report "gc_heap: queue order broken at " & integer'image(i)
          severity failure;
      end if;
    end loop;
    wait until falling_edge(clk);
    push <= '0';
    pop <= '0';
    wait until falling_edge(clk);
    assert count = 3
      report "gc_heap: queue holds " & integer'image(count)
      severity failure;
    done <= true;
    wait;
  end process stream;

  lists : process
    variable head : cell_ptr;
    variable l : line;
  begin
    for round in 1 to 40 loop
      head := build(500);
      assert sum(head) = 125250
        report "gc_heap: bad list sum in round " & integer'image(round)
        severity failure;
      head := null; -- the whole list is garbage now
      wait for 1 ns;
    end loop;
    wait until done;
    write(l, string'("gc_heap: queue and lists ok"));
    writeline(output, l);
    std.env.finish;
  end process lists;
end architecture sim;
