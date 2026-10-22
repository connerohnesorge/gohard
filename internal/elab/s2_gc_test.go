package elab

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// s2_gc_test.go pins the roots and the mirror handling of the #27
// collector (gc.go) that the K tests do not reach: the place of a
// pending out parameter, the protected object of a running method, and
// std.reflection mirrors, which are collected like any access value
// while a live mirror keeps the values it reflects. Each design runs
// with the collector forced at every allocation and must print what it
// prints with the collector off; deleting any of these roots from
// marker.roots, or the mirror marking, makes one of them fail.

// s2GCDesigns are the designs, by top.
var s2GCDesigns = map[string]string{
	// The only access to the node whose field is the actual of an out
	// parameter is dropped by the procedure before it writes the
	// parameter: the pending place keeps the node.
	"outs": `
use std.textio.all;
entity outs is end entity;
architecture a of outs is
  type node;
  type ptr is access node;
  type node is record
    v   : integer;
    nxt : ptr;
  end record;
begin
  process
    variable t, junk : ptr;
    variable l : line;
    procedure hold(variable o : out integer) is
    begin
      t := null;
      for i in 1 to 50 loop
        junk := new node'(i, null);
      end loop;
      o := 5;
    end procedure;
    variable keep : ptr;
  begin
    t := new node'(1, null);
    keep := t;
    hold(t.all.v);
    write(l, string'("outs "));
    write(l, keep.v);
    writeline(output, l);
    t := new node'(2, null);
    keep := null;
    hold(t.all.v);
    write(l, string'("outs done"));
    writeline(output, l);
    wait;
  end process;
end architecture;
`,
	// A method runs on a protected object whose last access value the
	// method itself drops (through another object): the method frame
	// keeps its object.
	"obj": `
use std.textio.all;
entity obj is end entity;
architecture a of obj is
  type iptr is access integer;
  type pk is protected
    procedure run;
  end protected;
  type pa is access pk;
  type holder is protected
    procedure set(x : pa);
    procedure clear;
    procedure go;
  end protected;
  type holder is protected body
    variable k : pa;
    procedure set(x : pa) is begin k := x; end procedure;
    procedure clear is begin k := null; end procedure;
    procedure go is begin k.run; end procedure;
  end protected body;
  shared variable h : holder;
  type pk is protected body
    variable n : integer := 0;
    procedure run is
      variable t : iptr;
      variable l : line;
    begin
      h.clear;
      for i in 1 to 50 loop
        t := new integer'(i);
      end loop;
      n := n + 8;
      write(l, string'("n="));
      write(l, n);
      writeline(output, l);
    end procedure;
  end protected body;
begin
  process
    variable p : pa;
  begin
    p := new pk;
    h.set(p);
    p := null;
    h.go;
    wait;
  end process;
end architecture;
`,
	// A value mirror of a list outlives the list's variable: the
	// mirror keeps the nodes it reflects, and gives the same mirrors
	// again for the same subtype.
	"mirror": `
use std.textio.all;
use std.reflection.all;
entity mirror is end entity;
architecture a of mirror is
  type node;
  type link is access node;
  type node is record
    v   : integer;
    nxt : link;
  end record;
begin
  process
    variable vm : value_mirror;
    variable s1, s2 : subtype_mirror;
    variable l, t : link;
    variable o : line;
  begin
    l := new node'(1, new node'(2, null));
    vm := l'reflect;
    s1 := vm.get_subtype_mirror;
    l := null;
    for i in 1 to 100 loop
      t := new node'(i, null);
    end loop;
    s2 := vm.get_subtype_mirror;
    write(o, string'("second node "));
    write(o, vm.to_access.get.to_record.get("nxt").
      to_access.get.to_record.get(0).to_integer.value);
    write(o, string'(" same "));
    write(o, s1 = s2);
    writeline(output, o);
    wait;
  end process;
end architecture;
`,
}

// TestGCRootsS2: each design prints the same with the collector forced
// at every allocation as with it off, and the forced runs collect.
func TestGCRootsS2(t *testing.T) {
	for _, top := range slices.Sorted(maps.Keys(s2GCDesigns)) {
		src := s2GCDesigns[top]
		off := kSim(t, src, top, kOpts{gc: GCOptions{Off: true}})
		if len(off.diags) > 0 || len(off.out) == 0 {
			t.Fatalf("%s off: %v %v", top, off.diags, off.out)
		}
		for _, m := range gcModes[1:] {
			r := kSim(t, src, top, kOpts{gc: m.gc})
			if len(r.diags) > 0 || !slices.Equal(r.out, off.out) ||
				r.steps != off.steps {
				t.Errorf("%s %s: %v %q, want %q (steps %d vs %d)", top,
					m.name, r.diags, r.out, off.out, r.steps, off.steps)
			}
			if m.gc.Every == 1 && r.stats[0].Collections == 0 {
				t.Errorf("%s: the forced collector never ran", top)
			}
		}
	}
}

// mirrorSoak reflects a fresh list n times, dropping each mirror.
func mirrorSoak(n int) string {
	return `
use std.textio.all;
use std.reflection.all;
entity msoak is end entity;
architecture a of msoak is
  type node;
  type link is access node;
  type node is record
    v   : integer;
    nxt : link;
  end record;
begin
  process
    variable vm : value_mirror;
    variable l : link;
    variable s : integer := 0;
    variable o : line;
  begin
    for i in 1 to ` + strconv.Itoa(n) + ` loop
      l := new node'(i, null);
      vm := l'reflect;
      s := s + vm.to_access.get.to_record.get(0).to_integer.value;
      if i mod 1000 = 0 then
        wait for 1 ns;
      end if;
    end loop;
    write(o, s);
    writeline(output, o);
    wait;
  end process;
end architecture;
`
}

// TestGCMirrorSoak (#27): mirrors made and dropped in a loop are
// collected: the live count is bounded by the collection period, not by
// the number of mirrors (twice the mirrors, the same bound), and the
// output is the collector-off output.
func TestGCMirrorSoak(t *testing.T) {
	var peaks []int64
	for _, n := range []int{6000, 12000} {
		src := mirrorSoak(n)
		r := kSim(t, src, "msoak", kOpts{})
		want := strconv.Itoa(n * (n + 1) / 2)
		if len(r.diags) > 0 || len(r.out) != 1 ||
			strings.TrimSpace(r.out[0]) != want {
			t.Fatalf("n=%d: %v %v, want %s", n, r.diags, r.out, want)
		}
		st := r.stats[0]
		if st.Collections == 0 || st.Live > 6*DefaultGCMin {
			t.Errorf("n=%d: %+v: mirrors were not collected", n, st)
		}
		peaks = append(peaks, st.Peak)
	}
	if peaks[1] > peaks[0]+DefaultGCMin {
		t.Errorf("peak grows with the number of mirrors: %v", peaks)
	}
}
