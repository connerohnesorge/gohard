package elab

import (
	"fmt"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
)

const fCounterDecl = `
  type counter_t is protected
    procedure add(by : integer);
    impure function get return integer;
  end protected counter_t;

  type counter_t is protected body
    variable n : integer := 0;
    procedure add(by : integer) is
    begin
      n := n + by;
    end procedure add;
    impure function get return integer is
    begin
      return n;
    end function get;
  end protected body counter_t;
`

// TestProtectedCompositeModel is a property: random method calls on
// the protected objects inside an array of records of arrays of
// protected objects, through process variables, a pointer to such a
// record and an array of pointers, leave every object at the sum a Go
// model of independent counters gives, so no two elements share an
// object and none is null.
func TestProtectedCompositeModel(t *testing.T) {
	names := []string{}
	for i := range 2 {
		for _, f := range []string{"a", "b"} {
			for k := range 3 {
				names = append(names, fmt.Sprintf("bank(%d).%s(%d)", i,
					f, k))
			}
		}
	}
	for k := range 3 {
		names = append(names, fmt.Sprintf("p.a(%d)", k),
			fmt.Sprintf("p.all.b(%d)", k))
	}
	names = append(names, "ps(0)", "ps(1).all", "single")
	for seed := range uint64(8) {
		r := &env.SplitMix64{State: seed}
		sum := map[string]int{}
		var body strings.Builder
		for range 40 {
			n := names[env.Intn(r, len(names))]
			by := env.Intn(r, 9) + 1
			sum[n] += by
			fmt.Fprintf(&body, "    %s.add(%d);\n", n, by)
		}
		var want []string
		for _, n := range names {
			fmt.Fprintf(&body, "    report \"%s = \" & "+
				"integer'image(%s.get);\n", n, n)
			want = append(want, fmt.Sprintf(
				"hint sim/assert-note report note: %s = %d", n, sum[n]))
		}
		src := `
entity tb is end entity tb;
architecture sim of tb is
` + fCounterDecl + `
  type counters_t is array (natural range <>) of counter_t;
  type pair_t is record
    a, b : counters_t(0 to 2);
  end record pair_t;
  type bank_t is array (0 to 1) of pair_t;
  type pair_ptr is access pair_t;
  type counter_ptr is access counter_t;
  type ptrs_t is array (0 to 1) of counter_ptr;
begin
  process
    variable bank   : bank_t;
    variable p      : pair_ptr;
    variable ps     : ptrs_t;
    variable single : counter_t;
  begin
    p := new pair_t;
    ps(0) := new counter_t;
    ps(1) := new counter_t;
` + body.String() + `
    wait;
  end process;
end architecture sim;
`
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			res, _ := simulate19(t, src, "tb")
			res.wantNotes(t, want...)
		})
	}
}

// TestProtectedDeallocate: deallocate frees the protected object a
// pointer designates; a copy of the pointer then designates nothing
// and its method call is a runtime error.
func TestProtectedDeallocate(t *testing.T) {
	src := `
entity tb is end entity tb;
architecture sim of tb is
` + fCounterDecl + `
  type counter_ptr is access counter_t;
begin
  process
    variable p, q : counter_ptr;
  begin
    p := new counter_t;
    p.add(5);
    q := p;
    report "q = " & integer'image(q.get);
    deallocate(p);
    report "q = " & integer'image(q.get);
    wait;
  end process;
end architecture sim;
`
	res, _ := simulate19(t, src, "tb")
	got := res.notes()
	if len(got) != 2 || got[0] != "hint sim/assert-note report note: q = 5" ||
		!strings.Contains(got[1], "sim/runtime-error") {
		t.Errorf("diagnostics %q", got)
	}
}

// TestPrivateVariables: private variables (VHDL-2019) are elaborated
// for every object, before the body's variables (a body initial value
// reads one), with their own initial values; a private variable of a
// protected type is an object of its own per enclosing object.
func TestPrivateVariables(t *testing.T) {
	src := `
entity tb is end entity tb;
architecture sim of tb is
` + fCounterDecl + `
  type stats_t is protected
    private variable base : integer := 100;
    private variable hits : counter_t;
    procedure hit;
    impure function total return integer;
  end protected stats_t;

  type stats_t is protected body
    variable start : integer := base + 1;
    procedure hit is
    begin
      hits.add(1);
      base := base + 1;
    end procedure hit;
    impure function total return integer is
    begin
      return start * 1000 + base * 10 + hits.get;
    end function total;
  end protected body stats_t;

  type stats_arr is array (0 to 1) of stats_t;
  shared variable s : stats_t;
begin
  process
    variable a : stats_arr;
  begin
    s.hit;
    a(1).hit;
    a(1).hit;
    report integer'image(s.total) & " " & integer'image(a(0).total) &
      " " & integer'image(a(1).total);
    wait;
  end process;
end architecture sim;
`
	res, _ := simulate19(t, src, "tb")
	res.wantNotes(t,
		"hint sim/assert-note report note: 102011 102000 102022")
}

// TestProtectedParams: functions with protected-type and access
// parameters run in the interpreter: a protected formal is the
// caller's object (no copy), an inout access formal assigned in the
// function is written back to its actual, and a formal of an array of
// protected type reaches each element.
func TestProtectedParams(t *testing.T) {
	src := `
entity tb is end entity tb;
architecture sim of tb is
` + fCounterDecl + `
  type counters_t is array (natural range <>) of counter_t;
  type int_ptr is access integer;

  impure function bump (variable c : inout counter_t; by : integer)
    return integer is
  begin
    c.add(by);
    return c.get;
  end function bump;

  impure function total (variable cs : inout counters_t)
    return integer is
    variable s : integer := 0;
  begin
    for i in cs'range loop
      cs(i).add(1);
      s := s + cs(i).get;
    end loop;
    return s;
  end function total;

  impure function fresh (variable p : inout int_ptr; v : integer)
    return integer is
  begin
    p := new integer'(v);
    return v + 1;
  end function fresh;

  shared variable c : counter_t;
begin
  process
    variable cs : counters_t(1 to 3);
    variable p  : int_ptr;
    variable r  : integer;
  begin
    r := bump(c, 4);
    r := bump(c, 5) + r;
    cs(2).add(10);
    report integer'image(r) & " " & integer'image(c.get) & " " &
      integer'image(total(cs)) & " " & integer'image(cs(2).get);
    r := fresh(p, 7);
    report integer'image(r) & " " & integer'image(p.all);
    wait;
  end process;
end architecture sim;
`
	res, _ := simulate19(t, src, "tb")
	res.wantNotes(t,
		"hint sim/assert-note report note: 13 9 13 11",
		"hint sim/assert-note report note: 8 7")
}
