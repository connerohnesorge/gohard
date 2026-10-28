package elab

import (
	"bytes"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/token"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// kRun is one run of the #27 collector tests.
type kRun struct {
	text  []byte   // the trace
	out   []string // OUTPUT lines
	diags []string
	steps int64
	stats []GCStats // GCOptions.Done, once per world
}

// kOpts are the options of a kSim run.
type kOpts struct {
	std   string // "": 2019
	seed  uint64
	steps int64
	gc    GCOptions
}

// kAnalyze analyzes src under std with the v1.1 detectors bypassed
// (see gAnalyze).
func kAnalyze(t testing.TB, src, std string) *sema.Result {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = std
	r := sema.Analyze(sema.Input{Config: cfg, Files: []sema.Source{{
		Path: "t.vhd", Text: []byte(src)}}})
	for _, u := range r.Units {
		u.Opaque, u.Unchecked = false, false
	}
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error &&
			d.Code != diag.UnsupportedConstruct &&
			d.Code != diag.SemaUnchecked {
			t.Fatalf("analysis: %s:%d:%d: %s", d.Path, d.Line, d.Col,
				d.Message)
		}
	}
	return r
}

// kSim simulates top of src to the end.
func kSim(t testing.TB, src, top string, o kOpts) kRun {
	t.Helper()
	if o.std == "" {
		o.std = "2019"
	}
	std, _ := token.ParseStandard(o.std)
	r := kAnalyze(t, src, o.std)
	var res kRun
	gc := o.gc
	gc.Done = func(s GCStats) { res.stats = append(res.stats, s) }
	code := Interpreter(InterpOptions{
		Output: func(l string) { res.out = append(res.out, l) },
		Env:    EnvOptions{Standard: std}, GC: gc})
	b := eval.NewBudget(o.steps)
	d, ds := Elaborate(r, Options{Top: top, Budget: b, Code: code})
	if d == nil {
		t.Fatalf("elaboration: %v", ds)
	}
	for _, x := range ds {
		if x.Severity == diag.Error {
			t.Fatalf("elaboration: %s", x.Message)
		}
	}
	rec := trace.NewRecorder(nil)
	opts := sim.Options{Budget: b, TimeBudget: 1_000_000_000_000_000,
		Writer: rec, Seed: o.seed}
	if o.seed != 0 {
		opts.Order = env.SeedOrder{Seed: o.seed}
	}
	k, err := sim.New(d.Model, opts)
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	for _, x := range k.Diagnostics() {
		res.diags = append(res.diags, string(x.Code)+": "+x.Message)
	}
	res.steps = k.Steps()
	if tr, err := rec.Trace(); err == nil {
		res.text = trace.Encode(tr)
	}
	return res
}

// soakSrc builds and drops a 100-node linked list rounds times
// (100*rounds allocations), with a procedure-local LINE written per
// round, an access to a protected counter dropped per round, and a
// wait every 10 rounds; it checks the list's sum every round.
func soakSrc(rounds int) string {
	return `
use std.textio.all;
entity soak is end entity;
architecture a of soak is
  type node;
  type ptr is access node;
  type node is record
    v    : integer;
    nxt  : ptr;
  end record;
  type counter is protected
    procedure inc;
    impure function get return integer;
  end protected;
  type counter is protected body
    variable n : integer := 0;
    procedure inc is begin n := n + 1; end procedure;
    impure function get return integer is begin return n; end function;
  end protected body;
  type cptr is access counter;
  procedure say(x : integer) is
    variable l : line;
  begin
    write(l, x);
  end procedure;
begin
  process
    variable head, p : ptr;
    variable c : cptr;
    variable s, total : integer := 0;
    variable l : line;
  begin
    for r in 1 to ` + strconv.Itoa(rounds) + ` loop
      head := null;
      for i in 1 to 99 loop
        head := new node'(i, head);
      end loop;
      c := new counter;
      p := head;
      s := 0;
      while p /= null loop
        s := s + p.v;
        c.inc;
        p := p.nxt;
      end loop;
      assert s = 4950 and c.get = 99 report "bad sum" severity failure;
      say(s);
      total := total + 1;
      if r mod 10 = 0 then
        wait for 1 ns;
      end if;
    end loop;
    write(l, "rounds " & integer'image(total));
    writeline(output, l);
    wait;
  end process;
end architecture;
`
}

// TestGCSoak: about 1e5 allocations that drop their references keep
// the live count bounded by the collection period, not by the number
// of allocations, and the run is the same with the collector off.
func TestGCSoak(t *testing.T) {
	gcSoak(t, 1000)
}

// TestGCSoakMillion is the #27 soak test: on the order of a million
// allocations, no growth of the live count.
func TestGCSoakMillion(t *testing.T) {
	if testing.Short() {
		t.Skip("1e6 allocations")
	}
	gcSoak(t, 10000)
}

func gcSoak(t *testing.T, rounds int) {
	r := kSim(t, soakSrc(rounds), "soak", kOpts{})
	if len(r.diags) > 0 {
		t.Fatalf("diagnostics: %v", r.diags)
	}
	if want := "rounds " + strconv.Itoa(rounds); !slices.Equal(r.out,
		[]string{want}) {
		t.Fatalf("output %q, want %q", r.out, want)
	}
	if len(r.stats) != 1 {
		t.Fatalf("stats reported %d times", len(r.stats))
	}
	s := r.stats[0]
	t.Logf("%+v", s)
	// Each round allocates 99 nodes, a counter and a LINE.
	if allocs := int64(rounds) * 101; s.Handles < allocs {
		t.Errorf("handles %d < %d allocations", s.Handles, allocs)
	}
	// The default period is DefaultGCMin allocations (live stays far
	// below it), so the heap never holds more than one period of
	// garbage plus the live list.
	if bound := int64(DefaultGCMin + 2*101); s.Peak > bound {
		t.Errorf("peak %d > %d: the heap grows", s.Peak, bound)
	}
	if s.Collections < int64(rounds)*101/DefaultGCMin {
		t.Errorf("only %d collections", s.Collections)
	}
	if s.Live > int64(DefaultGCMin+2*101) {
		t.Errorf("live %d at the end", s.Live)
	}
}

// TestGCSoakBounded: the peak does not depend on the number of
// allocations (twice the rounds, same peak bound), the property the
// soak test proves at scale.
func TestGCSoakBounded(t *testing.T) {
	peak := func(rounds int) int64 {
		r := kSim(t, soakSrc(rounds), "soak", kOpts{
			gc: GCOptions{Every: 256}})
		if len(r.diags) > 0 || len(r.stats) != 1 {
			t.Fatalf("%v %v", r.diags, r.stats)
		}
		return r.stats[0].Peak
	}
	a, b := peak(100), peak(400)
	if a > 256+2*101 || b > 256+2*101 {
		t.Errorf("peaks %d, %d exceed one period", a, b)
	}
	off := kSim(t, soakSrc(100), "soak", kOpts{gc: GCOptions{Off: true}})
	if p := off.stats[0].Peak; p < 100*100 {
		t.Errorf("collector off: peak %d, want every allocation", p)
	}
}

// gcModes are the collector settings every run must agree across.
var gcModes = []struct {
	name string
	gc   GCOptions
}{
	{"off", GCOptions{Off: true}},
	{"every", GCOptions{Every: 1}},
	{"every7", GCOptions{Every: 7}},
	{"default", GCOptions{}},
}

// gcDesigns are designs whose heap use differs: lists, trees of
// protected objects, DEALLOCATE mixed with dropped references, LINEs,
// function results and out parameters holding access values, shared
// variables and two processes sharing a heap object.
var gcDesigns = map[string]string{
	"soak": soakSrc(30),
	"mixed": `
use std.textio.all;
entity mixed is end entity;
architecture a of mixed is
  type node;
  type ptr is access node;
  type node is record
    v           : integer;
    left, right : ptr;
  end record;
  type pvec is array (natural range <>) of ptr;
  type box is protected
    procedure put(x : integer);
    impure function take return integer;
    procedure hold(x : ptr);
    impure function held return integer;
  end protected;
  type box is protected body
    variable p, k : ptr;
    procedure hold(x : ptr) is
    begin
      k := x;
    end procedure;
    impure function held return integer is
    begin
      if k = null then
        return -1;
      end if;
      return k.v;
    end function;
    procedure put(x : integer) is
    begin
      p := new node'(x, p, null);
    end procedure;
    impure function take return integer is
      variable q : ptr := p;
      variable v : integer;
    begin
      if q = null then
        return -1;
      end if;
      v := q.v;
      p := q.left;
      deallocate(q);
      return v;
    end function;
  end protected body;
  shared variable sb : box;
  impure function tree(d : integer) return ptr is
  begin
    if d = 0 then
      return null;
    end if;
    return new node'(d, tree(d - 1), tree(d - 1));
  end function;
  impure function count(p : ptr) return integer is
  begin
    if p = null then
      return 0;
    end if;
    return 1 + count(p.left) + count(p.right);
  end function;
  procedure grow(variable p : inout ptr; n : integer) is
  begin
    for i in 1 to n loop
      p := new node'(i, p, null);
    end loop;
  end procedure;
  signal tick : integer := 0;
begin
  a : process
    variable t : ptr;
    variable v : pvec(0 to 3);
    variable l : line;
  begin
    for r in 1 to 40 loop
      t := tree(5);
      assert count(t) = 31 report "tree" severity failure;
      for i in v'range loop
        v(i) := new node'(r * 10 + i, t, null);
      end loop;
      grow(v(r mod 4), 3);
      sb.put(r);
      if r mod 3 = 0 then
        sb.hold(v(1));
      end if;
      write(l, string'("r="));
      write(l, r);
      write(l, string'(" n="));
      write(l, count(v(r mod 4).left));
      writeline(output, l);
      tick <= r;
      wait for 1 ns;
    end loop;
    wait;
  end process;
  b : process
    variable got : integer;
    variable l : line;
  begin
    wait on tick;
    got := sb.take;
    if sb.held /= -1 then
      write(l, string'("keep "));
      write(l, sb.held);
      write(l, string'(" take "));
      write(l, got);
      writeline(output, l);
    end if;
    if tick = 40 then
      wait;
    end if;
  end process;
end architecture;
`,
}

// TestGCDeterministic: the trace bytes, the OUTPUT lines, the
// diagnostics and the step count are the same with the collector off,
// forced at every allocation, at another period and at the default,
// for every seed.
func TestGCDeterministic(t *testing.T) {
	for _, name := range slices.Sorted(maps.Keys(gcDesigns)) {
		src := gcDesigns[name]
		for _, seed := range []uint64{0, 1, 2, 99} {
			var base kRun
			for i, m := range gcModes {
				r := kSim(t, src, name, kOpts{seed: seed, gc: m.gc})
				if len(r.diags) > 0 {
					t.Fatalf("%s %s: %v", name, m.name, r.diags)
				}
				if i == 0 {
					base = r
					if len(r.out) == 0 {
						t.Fatalf("%s: no output", name)
					}
					continue
				}
				if !bytes.Equal(r.text, base.text) ||
					!slices.Equal(r.out, base.out) ||
					r.steps != base.steps {
					t.Errorf("%s seed %d: %s differs from off "+
						"(steps %d vs %d)", name, seed, m.name, r.steps,
						base.steps)
				}
				if m.gc.Every == 1 && r.stats[0].Collections == 0 {
					t.Errorf("%s: forced collector never ran", name)
				}
			}
		}
	}
}

// TestGCBudget: a run that exceeds simulation.step_budget stops at the
// same point, with the same trace, whatever the collector does: it
// charges no step.
func TestGCBudget(t *testing.T) {
	src := soakSrc(200)
	var base kRun
	for i, m := range gcModes {
		r := kSim(t, src, "soak", kOpts{steps: 20000, gc: m.gc})
		if !slices.ContainsFunc(r.diags, func(s string) bool {
			return strings.HasPrefix(s, string(diag.SimBudgetExceeded))
		}) {
			t.Fatalf("%s: no budget diagnostic: %v", m.name, r.diags)
		}
		if i == 0 {
			base = r
			continue
		}
		if !bytes.Equal(r.text, base.text) ||
			!slices.Equal(r.diags, base.diags) || r.steps != base.steps {
			t.Errorf("%s: budget run differs from off", m.name)
		}
	}
}

// TestGCReachable: nothing reachable is swept. Every object a design
// can still name survives forced collections, through every kind of
// root: process and procedure variables, protected object variables,
// shared variables, elements of composites, function results, out
// parameters and the designated values of access values.
func TestGCReachable(t *testing.T) {
	r := kSim(t, gcDesigns["mixed"], "mixed",
		kOpts{gc: GCOptions{Every: 1}})
	if len(r.diags) > 0 {
		t.Fatal(r.diags)
	}
	want := kSim(t, gcDesigns["mixed"], "mixed",
		kOpts{gc: GCOptions{Off: true}})
	if !slices.Equal(r.out, want.out) {
		t.Fatalf("output differs:\n%s\nwant\n%s",
			strings.Join(r.out, "\n"), strings.Join(want.out, "\n"))
	}
	if r.stats[0].Live >= want.stats[0].Live {
		t.Errorf("forced collections kept %d objects, off %d",
			r.stats[0].Live, want.stats[0].Live)
	}
}

// TestGCHandlesNotReused: after collections, new objects get new
// handles, and a dangling access value left by DEALLOCATE still
// reports a deallocated object, never another object's value.
func TestGCHandlesNotReused(t *testing.T) {
	src := `
entity tb is end entity;
architecture a of tb is
  type ptr is access integer;
begin
  process
    variable p, q : ptr;
  begin
    p := new integer'(1);
    q := p;
    deallocate(p);
    for i in 1 to 100 loop
      p := new integer'(i);
    end loop;
    report "p " & integer'image(p.all);
    report "q " & integer'image(q.all);
    wait;
  end process;
end architecture;
`
	r := kSim(t, src, "tb", kOpts{gc: GCOptions{Every: 1}})
	if len(r.stats) != 1 || r.stats[0].Collections == 0 {
		t.Fatalf("stats %v", r.stats)
	}
	if !slices.ContainsFunc(r.diags, func(s string) bool {
		return strings.Contains(s, "deallocated access value")
	}) {
		t.Errorf("dangling q.all: %v", r.diags)
	}
	// A world's handles only grow.
	w := newWorld(&Design{}, InterpOptions{})
	seen := map[int64]bool{}
	for range 1000 {
		v := w.alloc(nil, eval.Value{})
		if seen[v.I] {
			t.Fatalf("handle %d reused", v.I)
		}
		seen[v.I] = true
		if v.I%10 == 0 {
			w.collect() // no roots: everything goes
		}
	}
	if w.live() >= 10 {
		t.Errorf("live %d after collections", w.live())
	}
}

// TestGCCloneAfterCollection: a kernel cloned after collections
// continues exactly like the original (TestCloneContinues with the
// collector running before and after the cut).
func TestGCCloneAfterCollection(t *testing.T) {
	src := soakSrc(60)
	r := kAnalyze(t, src, "2019")
	var stats []GCStats
	code := Interpreter(InterpOptions{
		Env: EnvOptions{Standard: token.Std2019},
		GC: GCOptions{Every: 50, Done: func(s GCStats) {
			stats = append(stats, s)
		}}})
	d, ds := Elaborate(r, Options{Top: "soak", Code: code})
	if d == nil {
		t.Fatal(ds)
	}
	for _, cut := range []int{1, 2, 3, 5} {
		stats = nil
		k, err := sim.New(d.Model, sim.Options{})
		if err != nil {
			t.Fatal(err)
		}
		k.Run(cut)
		c := k.Clone()
		k.RunAll(64, nil)
		c.RunAll(64, nil)
		if k.Status() != c.Status() || k.Now() != c.Now() ||
			k.Err() != nil || c.Err() != nil {
			t.Fatalf("cut %d: %v at %d vs %v at %d (%v %v)", cut,
				k.Status(), k.Now(), c.Status(), c.Now(), k.Err(), c.Err())
		}
		if len(stats) != 2 || stats[0] != stats[1] {
			t.Errorf("cut %d: stats %+v", cut, stats)
		}
		if len(stats) > 0 && stats[0].Collections == 0 {
			t.Errorf("cut %d: no collection", cut)
		}
	}
}

// TestGCNoFunctionSafePoint: a function that allocates in a loop keeps
// its garbage until it returns (no safe point while it runs), and the
// result it returns survives the next collection.
func TestGCNoFunctionSafePoint(t *testing.T) {
	src := `
entity tb is end entity;
architecture a of tb is
  type ptr is access integer;
  impure function churn(n : integer) return ptr is
    variable p : ptr;
  begin
    for i in 1 to n loop
      p := new integer'(i);
    end loop;
    return p;
  end function;
begin
  process
    variable p : ptr;
  begin
    p := churn(500);
    p := new integer'(p.all + 1);
    report integer'image(p.all);
    wait;
  end process;
end architecture;
`
	var msgs []string
	r := kSim(t, src, "tb", kOpts{gc: GCOptions{Every: 1}})
	for _, d := range r.diags {
		msgs = append(msgs, d)
	}
	if len(msgs) != 1 || !strings.HasSuffix(msgs[0], "501") {
		t.Fatalf("diags %v", msgs)
	}
	s := r.stats[0]
	if s.Peak < 500 || s.Live > 2 {
		t.Errorf("stats %+v: want a peak of the loop, then 1 or 2 live", s)
	}
}
