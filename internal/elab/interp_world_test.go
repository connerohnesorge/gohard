package elab

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
)

// worldRun is a simulation whose world the test can inspect.
type worldRun struct {
	w     *world
	d     *Design
	k     *sim.Kernel
	out   []string
	diags []diag.Diagnostic
}

// simWorld simulates src with top under standard std and order seed
// (0: the default order). Analysis errors other than the v1.0
// unsupported/construct detectors fail the test: the constructs this
// stage gives a run-time model (composites of and pointers to
// protected types) still carry them.
func simWorld(t *testing.T, src, top, std string, seed uint64) worldRun {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = std
	r := sema.Analyze(sema.Input{Config: cfg, Files: []sema.Source{{
		Path: "t.vhd", Text: []byte(src)}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error && d.Code != diag.UnsupportedConstruct {
			t.Fatalf("analysis: %d:%d: %s", d.Line, d.Col, d.Message)
		}
	}
	var res worldRun
	o := InterpOptions{Output: func(l string) { res.out = append(res.out, l) }}
	code := func(d *Design, p *Proc) (sim.Code, error) {
		if res.w == nil {
			res.w = newWorld(d, o)
		}
		return codeIn(res.w, p)
	}
	b := eval.NewBudget(0)
	d, ds := Elaborate(r, Options{Top: top, Budget: b, Code: code})
	if d == nil {
		t.Fatalf("elaboration: %v", ds)
	}
	for _, x := range ds {
		if x.Severity == diag.Error {
			t.Fatalf("elaboration: %s", x.Message)
		}
	}
	opts := sim.Options{Budget: b, TimeBudget: 1_000_000_000_000}
	if seed != 0 {
		opts.Seed = seed
		opts.Order = env.SeedOrder{Seed: seed}
	}
	k, err := sim.New(d.Model, opts)
	if err != nil {
		t.Fatal(err)
	}
	// The kernel runs clones of the codes: their shared world is the
	// copy the cloning pass made.
	if res.w != nil && res.w.copy != nil {
		res.w = res.w.copy
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	res.d, res.k, res.diags = d, k, k.Diagnostics()
	return res
}

func (r worldRun) clean(t *testing.T) {
	t.Helper()
	for _, d := range r.diags {
		if d.Severity != diag.Hint {
			t.Errorf("%s %s: %s", d.Severity, d.Code, d.Message)
		}
	}
}

func (r worldRun) wantOut(t *testing.T, want ...string) {
	t.Helper()
	if !slices.Equal(r.out, want) {
		t.Errorf("OUTPUT:\n%s\nwant:\n%s", strings.Join(r.out, "\n"),
			strings.Join(want, "\n"))
	}
}

// handles returns the protected-object handle of every shared
// variable, by instance path and name, in creation order.
func (r worldRun) handles() []string {
	var out []string
	for _, k := range r.w.sharedOrder() {
		s, ok := r.w.shared[k]
		if !ok || s.v.T == nil || s.v.T.Kind != eval.KindProtected {
			continue
		}
		path := "pkg"
		if k.in != nil {
			path = k.in.Path
		}
		out = append(out, path+"."+k.d.Text+"="+
			strconv.FormatInt(s.v.I, 10))
	}
	return out
}

const counterPkg = `
package cnt_pkg is
  type counter is protected
    procedure inc;
    impure function get return integer;
  end protected;
end package;
package body cnt_pkg is
  type counter is protected body
    variable n : integer := 0;
    procedure inc is begin n := n + 1; end procedure;
    impure function get return integer is begin return n; end function;
  end protected body;
end package body;
use work.cnt_pkg.all;
package glob_pkg is
  shared variable global : counter;
end package;
use work.glob_pkg.all;
`

// twoInstances instantiates an architecture with shared variables
// twice (and a generate with two iterations): each instance owns its
// objects, initialised with its own generics.
const twoInstances = counterPkg + `
use std.textio.all;
use work.cnt_pkg.all;
entity unit is generic (k : integer); end entity;
architecture a of unit is
  type based is protected
    procedure inc;
    impure function get return integer;
  end protected;
  type based is protected body
    variable n : integer := k * 10;
    procedure inc is begin n := n + 1; end procedure;
    impure function get return integer is begin return n; end function;
  end protected body;
  shared variable c : counter;
  shared variable base : based;
  procedure say(msg : string) is
    variable l : line;
  begin
    write(l, msg);
    writeline(output, l);
  end procedure;
begin
  p : process
  begin
    for i in 1 to k loop
      c.inc;
      global.inc;
    end loop;
    base.inc;
    wait for k * 1 ns;
    say(integer'image(k) & " " & integer'image(c.get) & " " &
      integer'image(base.get));
    wait;
  end process;
end architecture;

use std.textio.all;
use work.cnt_pkg.all, work.glob_pkg.all;
entity tb is end entity;
architecture a of tb is
begin
  u1 : entity work.unit generic map (k => 1);
  u2 : entity work.unit generic map (k => 2);
  g : for j in 3 to 4 generate
    shared variable gc : counter;
  begin
    u : entity work.unit generic map (k => j);
    q : process begin gc.inc; wait; end process;
  end generate;
  fin : process
    variable l : line;
  begin
    wait for 10 ns;
    write(l, "global " & integer'image(global.get));
    writeline(output, l);
    wait;
  end process;
end architecture;
`

// TestSharedPerInstance: a shared variable of an architecture
// instantiated twice is two objects (audit "protected" risk 1), and
// whatever the process order, every shared object gets the same
// handle: they are created before any process runs, in sharedOrder.
func TestSharedPerInstance(t *testing.T) {
	src := twoInstances
	want := []string{"1 1 11", "2 2 21", "3 3 31", "4 4 41", "global 10"}
	var first []string
	for seed := range uint64(6) {
		r := simWorld(t, src, "tb", "2008", seed)
		r.clean(t)
		r.wantOut(t, want...)
		h := r.handles()
		if seed == 0 {
			first = h
			// The package object first, then u1, u2, g(3), g(3).u,
			// g(4), g(4).u: handles 1..11 in that order.
			wantH := []string{"pkg.global=1", "tb.u1.c=2",
				"tb.u1.base=3", "tb.u2.c=4", "tb.u2.base=5",
				"tb.g(3).gc=6", "tb.g(3).u.c=7", "tb.g(3).u.base=8",
				"tb.g(4).gc=9", "tb.g(4).u.c=10", "tb.g(4).u.base=11"}
			if !slices.Equal(h, wantH) {
				t.Fatalf("handles %v, want %v", h, wantH)
			}
			continue
		}
		if !slices.Equal(h, first) {
			t.Errorf("seed %d: handles %v, want %v", seed, h, first)
		}
	}
}

// TestSharedKeys: the slots are keyed by (instance, declaration): the
// same declaration has one slot per instance and every protected
// handle is distinct (never shared, never reused).
func TestSharedKeys(t *testing.T) {
	r := simWorld(t, twoInstances, "tb", "2008", 0)
	r.clean(t)
	byDecl := map[string]int{}
	seen := map[int64]bool{}
	vars := 0
	for k, s := range r.w.shared {
		if k.d.Kind != sema.DeclSharedVariable {
			continue // std.textio's OUTPUT
		}
		vars++
		byDecl[k.d.Text]++
		if s.v.T != nil && s.v.T.Kind == eval.KindProtected {
			if seen[s.v.I] || s.v.I == 0 {
				t.Errorf("handle %d of %s reused or null", s.v.I, k.d.Text)
			}
			seen[s.v.I] = true
			if _, ok := r.w.objs[s.v.I]; !ok {
				t.Errorf("%s: no object %d", k.d.Text, s.v.I)
			}
		}
	}
	want := map[string]int{"global": 1, "c": 4, "base": 4, "gc": 2}
	for name, n := range want {
		if byDecl[name] != n {
			t.Errorf("%s: %d slots, want %d", name, byDecl[name], n)
		}
	}
	if vars != 11 {
		t.Errorf("%d shared variables, want 11", vars)
	}
}

// TestSharedExternalName: an external name of a shared variable
// reaches the object of the instance it names.
func TestSharedExternalName(t *testing.T) {
	src := counterPkg + `
use work.cnt_pkg.all;
entity unit is generic (k : integer); end entity;
architecture a of unit is
  shared variable c : counter;
begin
  p : process begin
    for i in 1 to k loop c.inc; end loop;
    wait;
  end process;
end architecture;

use std.textio.all;
use work.cnt_pkg.all;
entity tb is end entity;
architecture a of tb is
begin
  u1 : entity work.unit generic map (k => 1);
  u2 : entity work.unit generic map (k => 5);
  p : process
    alias c1 is << variable .tb.u1.c : counter >>;
    alias c2 is << variable .tb.u2.c : counter >>;
    variable l : line;
  begin
    wait for 1 ns;
    write(l, integer'image(c1.get) & " " & integer'image(c2.get));
    writeline(output, l);
    wait;
  end process;
end architecture;
`
	r := simWorld(t, src, "tb", "2008", 0)
	r.clean(t)
	r.wantOut(t, "1 5")
}

// protComposites uses arrays and records of protected objects, a
// protected body holding an array of objects, and pointers to
// objects (VHDL-2019; the v1.0 detectors still flag them).
const protComposites = `
use std.textio.all;
entity tb is end entity;
architecture a of tb is
  type counter is protected
    procedure inc;
    impure function get return integer;
  end protected;
  type counter is protected body
    variable n : integer := 0;
    procedure inc is begin n := n + 1; end procedure;
    impure function get return integer is begin return n; end function;
  end protected body;
  type arr is array (0 to 2) of counter;
  type rec is record
    a : counter;
    k : integer;
    b : arr;
  end record;
  type ptr is access counter;
  type bank is protected
    procedure inc(i : integer);
    impure function get(i : integer) return integer;
  end protected;
  type bank is protected body
    variable cs : arr;
    procedure inc(i : integer) is begin cs(i).inc; end procedure;
    impure function get(i : integer) return integer is
    begin
      return cs(i).get;
    end function;
  end protected body;
  shared variable sa : arr;
  procedure say(msg : string) is
    variable l : line;
  begin
    write(l, msg);
    writeline(output, l);
  end procedure;
begin
  p : process
    variable a : arr;
    variable r : rec;
    variable p1, p2 : ptr;
    variable bk : bank;
  begin
    a(1).inc;
    a(2).inc;
    a(2).inc;
    say("arr " & integer'image(a(0).get) & integer'image(a(1).get) &
      integer'image(a(2).get));
    r.a.inc;
    r.b(0).inc;
    r.b(0).inc;
    r.b(2).inc;
    say("rec " & integer'image(r.a.get) & integer'image(r.b(0).get) &
      integer'image(r.b(1).get) & integer'image(r.b(2).get));
    p1 := new counter;
    p2 := new counter;
    p1.inc;
    p1.inc;
    p2.inc;
    say("ptr " & integer'image(p1.get) & integer'image(p2.get));
    bk.inc(0);
    bk.inc(2);
    bk.inc(2);
    say("bank " & integer'image(bk.get(0)) & integer'image(bk.get(1)) &
      integer'image(bk.get(2)));
    sa(0).inc;
    say("shared " & integer'image(sa(0).get) & integer'image(sa(1).get));
    deallocate(p1);
    say("freed " & boolean'image(p1 = null));
    wait;
  end process;
end architecture;
`

// TestInstantiateComposites: every protected element of an array or
// record, in a variable, a shared variable, a protected body or an
// allocated object, is its own object.
func TestInstantiateComposites(t *testing.T) {
	r := simWorld(t, protComposites, "tb", "2019", 0)
	r.clean(t)
	r.wantOut(t, "arr 012", "rec 1201", "ptr 21", "bank 102", "shared 10",
		"freed true")
	// sa (3), a (3), r (1 + 3), p1, p2, bk (1 + 3): 16 objects made,
	// p1's freed.
	if n := len(r.w.objs); n != 15 {
		t.Errorf("%d protected objects alive, want 15", n)
	}
}

// TestDeallocateFreesObject: deallocating a pointer to a protected
// object frees the object and the objects its variables hold; heap
// cells the object points to stay.
func TestDeallocateFreesObject(t *testing.T) {
	src := `
entity tb is end entity;
architecture a of tb is
  type counter is protected
    procedure inc;
  end protected;
  type counter is protected body
    variable n : integer := 0;
    procedure inc is begin n := n + 1; end procedure;
  end protected body;
  type arr is array (0 to 1) of counter;
  type holder is protected
    procedure inc;
  end protected;
  type holder is protected body
    variable cs : arr;
    variable one : counter;
    procedure inc is begin cs(1).inc; one.inc; end procedure;
  end protected body;
  type hptr is access holder;
  type iptr is access integer;
begin
  p : process
    variable h : hptr;
    variable i : iptr;
  begin
    h := new holder;
    i := new integer'(3);
    h.inc;
    deallocate(h);
    wait;
  end process;
end architecture;
`
	r := simWorld(t, src, "tb", "2019", 0)
	r.clean(t)
	if n := len(r.w.objs); n != 0 {
		t.Errorf("%d protected objects alive, want 0", n)
	}
	if n := len(r.w.heap); n != 1 {
		t.Errorf("%d heap cells alive, want 1 (the integer)", n)
	}
}

// TestDerefFreedNoHandle: the error of a dereference of a deallocated
// access value does not print the handle.
func TestDerefFreedNoHandle(t *testing.T) {
	src := `
entity tb is end entity;
architecture a of tb is
  type iptr is access integer;
begin
  p : process
    variable a, b : iptr;
    variable x : integer;
  begin
    a := new integer'(1);
    b := new integer'(2);
    a := b;
    deallocate(b);
    x := a.all;
    wait;
  end process;
end architecture;
`
	r := simWorld(t, src, "tb", "2008", 0)
	var msg string
	for _, d := range r.diags {
		if d.Code == diag.SimRuntimeError {
			msg = d.Message
		}
	}
	if !strings.Contains(msg, "dereference of a deallocated access value") {
		t.Fatalf("diagnostics %v", r.diags)
	}
	i := strings.Index(msg, "access value")
	if rest := msg[i:]; strings.ContainsAny(rest[:min(len(rest), 16)],
		"0123456789") {
		t.Errorf("handle printed: %q", msg)
	}
}

// TestFunctionOutWriteBack: a VHDL-2019 function's inout variable
// parameter (here an access value) is written back to its actual.
func TestFunctionOutWriteBack(t *testing.T) {
	src := `
use std.textio.all;
entity tb is end entity;
architecture a of tb is
  type iptr is access integer;
  impure function take(variable p : inout iptr) return integer is
    variable v : integer;
  begin
    v := p.all;
    deallocate(p);
    p := new integer'(v + 1);
    return v;
  end function;
  function bump(variable p : inout iptr) return integer is
  begin
    p.all := p.all * 2;
    return p.all;
  end function;
begin
  p : process
    variable q : iptr := new integer'(4);
    variable r, s : integer;
    variable l : line;
  begin
    r := take(q);
    s := bump(q);
    write(l, integer'image(r) & " " & integer'image(s) & " " &
      integer'image(q.all));
    writeline(output, l);
    wait;
  end process;
end architecture;
`
	r := simWorld(t, src, "tb", "2019", 0)
	r.clean(t)
	r.wantOut(t, "4 10 10")
}

// TestRunsHere: calls with protected or access parameters, or out and
// inout variable parameters, run in the interpreter.
func TestRunsHere(t *testing.T) {
	prot := &sema.Type{Class: sema.TypeProtected}
	acc := &sema.Type{Class: sema.TypeAccess}
	in := &sema.Type{Class: sema.TypeInteger}
	fn := func(ps ...*sema.Decl) *sema.Decl {
		return &sema.Decl{Kind: sema.DeclFunction, Pure: true, Params: ps}
	}
	param := func(t *sema.Type, mode sema.Mode) *sema.Decl {
		return &sema.Decl{Kind: sema.DeclParameter, Type: t, Mode: mode,
			Class: sema.ClassVariable}
	}
	cases := []struct {
		x    *sema.Decl
		want bool
	}{
		{fn(), false},
		{fn(param(in, sema.ModeIn)), false},
		{fn(param(prot, sema.ModeInout)), true},
		{fn(param(acc, sema.ModeIn)), true},
		{fn(param(in, sema.ModeInout)), true},
		{fn(param(in, sema.ModeOut)), true},
		{&sema.Decl{Kind: sema.DeclFunction}, true}, // impure
	}
	for i, c := range cases {
		if got := runsHere(c.x); got != c.want {
			t.Errorf("case %d: runsHere %v, want %v", i, got, c.want)
		}
	}
}

// TestHoldsProtected is a property over composite nesting: a subtype
// holds a protected object exactly when a protected type is reachable
// through array elements and record fields, never through an access.
func TestHoldsProtected(t *testing.T) {
	w := &world{prot: map[*sema.Type]bool{}}
	prot := &sema.Type{Class: sema.TypeProtected}
	scalar := &sema.Type{Class: sema.TypeInteger}
	arrOf := func(e *sema.Type) *sema.Type {
		return &sema.Type{Class: sema.TypeArray, Elem: e}
	}
	recOf := func(fs ...*sema.Type) *sema.Type {
		r := &sema.Type{Class: sema.TypeRecord}
		for i, f := range fs {
			r.Fields = append(r.Fields, &sema.Decl{Type: f, Pos: i})
		}
		return r
	}
	accOf := func(e *sema.Type) *sema.Type {
		return &sema.Type{Class: sema.TypeAccess, Designated: e}
	}
	// Every nesting up to depth 3 of the four constructors over a
	// leaf, checked against the definition.
	type tc struct {
		t    *sema.Type
		want bool
	}
	level := []tc{{prot, true}, {scalar, false}}
	all := slices.Clone(level)
	for range 3 {
		var next []tc
		for _, c := range level {
			next = append(next, tc{arrOf(c.t), c.want},
				tc{recOf(scalar, c.t), c.want}, tc{recOf(c.t), c.want},
				tc{accOf(c.t), false})
		}
		all = append(all, next...)
		level = next
	}
	for i, c := range all {
		if got := w.holdsProtected(c.t); got != c.want {
			t.Errorf("type %d: holdsProtected %v, want %v", i, got,
				c.want)
		}
		if got := w.holdsProtected(c.t); got != c.want {
			t.Errorf("type %d: memoized %v, want %v", i, got, c.want)
		}
	}
	if w.holdsProtected(nil) {
		t.Error("nil holds a protected object")
	}
}

// TestOutParamDefaultInstantiated: an out parameter whose type holds a
// protected object starts at its subtype's default (LRM 4.2.2.1) with
// objects of its own: the procedure sees fresh counters, never the
// actual's; the copy back gives the actual the procedure's object.
func TestOutParamDefaultInstantiated(t *testing.T) {
	const src = `
use std.textio.all;
entity tb is end entity;
architecture a of tb is
  type counter is protected
    procedure inc;
    impure function get return integer;
  end protected;
  type counter is protected body
    variable n : integer := 0;
    procedure inc is begin n := n + 1; end procedure;
    impure function get return integer is begin return n; end function;
  end protected body;
  type rec is record
    c : counter;
    k : integer;
  end record;
  procedure say(msg : string) is
    variable l : line;
  begin
    write(l, msg);
    writeline(output, l);
  end procedure;
  procedure fill(variable o : out rec) is
  begin
    say("in " & integer'image(o.c.get));
    o.c.inc;
    say("after " & integer'image(o.c.get));
  end procedure;
begin
  p : process
    variable r : rec;
  begin
    r.c.inc;
    r.c.inc;
    fill(r);
    say("actual " & integer'image(r.c.get));
    wait;
  end process;
end architecture;
`
	r := simWorld(t, src, "tb", "2019", 0)
	r.clean(t)
	r.wantOut(t, "in 0", "after 1", "actual 1")
	if n := len(r.w.objs); n < 2 {
		t.Errorf("%d protected objects, want the actual's and the "+
			"default's", n)
	}
}
