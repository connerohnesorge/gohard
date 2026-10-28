package psl

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/elab"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// builtinSrc is the #111 reproducer with the directive DIR, its
// signals a and b of type TYPE, and three more signals: s and i change
// at 1 ns, before the first tick (5 ns), and v takes every STD_ULOGIC
// value at one of its elements.
const builtinSrc = `library ieee; use ieee.std_logic_1164.all;
entity s is end;
architecture rtl of s is
  signal clk : std_logic := '0';
  signal a, b : TYPE := '0';
  signal s : std_logic := '0';
  signal i : integer := 7;
  signal v : std_logic_vector(3 downto 0) := "0001";
  default clock is rising_edge(clk);
begin
  clk <= not clk after 5 ns;
  a <= '1' after 20 ns;
  b <= '1' after 30 ns;
  s <= '1' after 1 ns;
  i <= 8 after 1 ns;
  v <= "0H00" after 10 ns, "0L00" after 20 ns, "0100" after 30 ns,
    "0Z00" after 40 ns, "0W00" after 50 ns, "0-00" after 60 ns,
    "0U00" after 70 ns, "0001" after 80 ns;
  x: DIR
  stopper: process begin wait for 100 ns; std.env.finish; end process;
end;
`

// ticks are the clock ticks from 5 ns to 95 ns, from the k-th on.
func ticks(k int) []int64 {
	var out []int64
	for t := int64(5 + 10*k); t < 100; t += 10 {
		out = append(out, t)
	}
	return out
}

// TestBuiltinsNVC (#111): a directive using prev, stable, rose, fell,
// onehot, onehot0 or isunknown simulates, and fails (or is hit) at the
// ticks NVC 1.23.0 gives (nvc --std=2008 -a s.vhd -e s -r on
// builtinSrc). The first eight are the rows of the issue, run with
// STD_LOGIC and with BIT signals; NVC crashes on rose and fell of a
// STD_ULOGIC ("argument to not is not boolean"), so those rows take
// the ticks NVC gives with BIT. The arguments are sampled at time 0
// (the initial values) and at every tick; before the n-th sample
// prev(e, n) is the default value of e's subtype ('U' for STD_ULOGIC).
// In the last two GoHard differs from NVC on purpose: prev(prev(a))
// is prev(a, 2) by the definition of prev (NVC computes prev(a) and
// fails at 35 ns), and before the second sample prev(i, 2) is the
// default value of i's subtype, INTEGER'LEFT (NVC gives 0 and fails at
// 5 ns).
func TestBuiltinsNVC(t *testing.T) {
	rows := []struct {
		dir  string
		want []int64
	}{
		{"assert always (prev(a) = '1' -> b = '1');", nil},
		{"assert always (prev(a) = '1');", []int64{5, 15, 25}},
		{"assert always (stable(a));", []int64{25}},
		{"assert always (rose(a));", slices.Delete(ticks(0), 2, 3)},
		{"assert always (fell(a));", ticks(0)},
		{"assert always (rose(a) -> b);", []int64{25}},
		{"assert always (a -> stable(b));", []int64{35}},
		{"assert always (fell(a) -> b = '1');", nil},
	}
	for _, ty := range []string{"std_logic", "bit"} {
		for _, r := range rows {
			src := strings.NewReplacer("TYPE", ty, "DIR", r.dir).
				Replace(builtinSrc)
			checkTicks(t, ty+": "+r.dir, src, r.want)
		}
	}
	more := []struct {
		dir  string
		want []int64
	}{
		{"assert always (prev(s) = '0');", ticks(1)},
		{"assert always (prev(s) = '1');", []int64{5}},
		{"assert always (stable(s));", []int64{5}},
		{"assert always (prev(s, 2) = 'U');", ticks(1)},
		{"assert always (prev(s, 3) = 'U');", ticks(2)},
		{"assert always (prev(i) = 7);", ticks(1)},
		{"assert always (prev(a, 2) = '0');", ticks(4)},
		{"assert always (onehot(v));", []int64{25, 45, 55, 65, 75}},
		{"assert always (onehot0(v));", nil},
		{"assert always (isunknown(v) -> false);", []int64{45, 55, 65, 75}},
		{"cover {rose(a); b};", []int64{35}},
		{"assert always (prev(prev(a)) = prev(a, 2));", nil},
		{"assert always (prev(i, 2) /= 0);", nil},
	}
	for _, r := range more {
		src := strings.NewReplacer("TYPE", "bit", "DIR", r.dir).
			Replace(builtinSrc)
		checkTicks(t, r.dir, src, r.want)
	}
}

// checkTicks runs src and requires the directive's failures (or cover
// hits) at exactly the ticks want, in ns, and no other simulation
// diagnostic than the directive's own.
func checkTicks(t *testing.T, name, src string, want []int64) {
	t.Helper()
	evs, ds := simBuiltin(t, name, src)
	var got []int64
	for _, e := range evs {
		if e.Kind == trace.Assert || e.Kind == trace.CoverHit {
			got = append(got, int64(e.Time)/ns)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: ticks %v, want %v", name, got, want)
	}
	for _, d := range ds {
		switch d.Code {
		case diag.SimAssertError, diag.SimCoverHit:
		default:
			t.Errorf("%s: %s %s", name, d.Code, d.Message)
		}
	}
}

// simBuiltin analyzes, elaborates and simulates src (top s, VHDL-2008)
// with the real interpreter, running on past error assertions, and
// returns the trace events and the simulation diagnostics.
func simBuiltin(t *testing.T, name, src string) ([]trace.Event,
	[]diag.Diagnostic,
) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = "2008"
	r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
		Files: []sema.Source{{Path: "s.vhd", Text: []byte(src)}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("%s: analysis: %s %s", name, d.Code, d.Message)
		}
	}
	b := eval.NewBudget(0)
	d, ds := elab.Elaborate(r, elab.Options{Top: "s", Budget: b,
		Code: Wrap(elab.Interpreter(elab.InterpOptions{}))})
	if d == nil || len(ds) > 0 || len(d.Missing) > 0 {
		t.Fatalf("%s: elaborate: %v", name, ds)
	}
	rec := trace.NewRecorder(nil)
	k, err := sim.New(d.Model, sim.Options{Budget: b, Writer: rec,
		StopLevel: trace.Failure, TimeBudget: 1_000_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	return tr.Events, k.Diagnostics()
}

// TestBuiltinUnsupported (#111): a built-in call the simulator does not
// run makes its directive report unsupported/construct at time 0
// instead of failing at run time (sim/runtime-error).
func TestBuiltinUnsupported(t *testing.T) {
	cases := []struct{ dir, want string }{
		{"assert always (prev(a, 1, rising_edge(clk)) = '1' -> b);",
			"the clock argument of prev"},
		{"assert always (rose(a, rising_edge(clk)) -> b);",
			"the clock argument of rose"},
		{"assert always (stable(a, rising_edge(clk)));",
			"the clock argument of stable"},
		{"assert always (a -> b) @ (prev(clk) = '0');",
			"the built-in function prev in a clock expression"},
		{"assert always {a; b} |=> ended({a; b});",
			"the built-in function ended"},
		{"assert always (prev(a, 0) = '1');", "the count 0 of prev"},
	}
	for _, c := range cases {
		src := strings.NewReplacer("TYPE", "bit", "DIR", c.dir).
			Replace(builtinSrc)
		evs, ds := simBuiltin(t, c.dir, src)
		var got []string
		for _, d := range ds {
			got = append(got, string(d.Code)+" "+strconv.Itoa(d.Line)+
				" "+d.Message)
		}
		want := []string{"unsupported/construct 19 " + c.want +
			" in a PSL directive is not supported yet, please report"}
		if !slices.Equal(got, want) || len(evs) != 0 {
			t.Errorf("%s: %v, events %v", c.dir, got, evs)
		}
	}
}

// TestBuiltinSampleBound (#111, P7): a built-in call whose samples
// would keep more than maxSampled values (its depth times the size of
// one value of its argument) is a run-time error at its first sample,
// time 0, before it keeps any; a call just under the bound runs. One
// value of mem is 1 + 1024*33 = 33793 values, so the bound allows
// prev(mem, 31) (1047583 values) and not prev(mem, 32) (1081376).
func TestBuiltinSampleBound(t *testing.T) {
	const src = `library ieee; use ieee.std_logic_1164.all;
entity s is end;
architecture rtl of s is
  type mem_t is array (0 to 1023) of std_logic_vector(31 downto 0);
  signal clk : std_logic := '0';
  signal mem : mem_t := (others => (others => '0'));
  default clock is rising_edge(clk);
begin
  clk <= not clk after 5 ns;
  mem(0) <= not mem(0) after 10 ns;
  x: assert always (prev(mem, DEPTH) /= mem or true);
  stopper: process begin wait for 100 ns; std.env.finish; end process;
end;
`
	under := strings.ReplaceAll(src, "DEPTH", "31")
	checkTicks(t, "prev(mem, 31)", under, nil)
	over := strings.ReplaceAll(src, "DEPTH", "32")
	evs, ds := simBuiltin(t, "prev(mem, 32)", over)
	var got []string
	for _, d := range ds {
		got = append(got, string(d.Code)+" "+strconv.Itoa(d.Line)+
			" "+d.Message)
	}
	want := []string{"sim/runtime-error 11 prev(mem, 32) in PSL " +
		"directive x would keep more than 1048576 sampled values in s.x " +
		"at 0 fs"}
	if !slices.Equal(got, want) || len(evs) != 0 {
		t.Fatalf("prev(mem, 32): %q, events %v", got, evs)
	}
}

// TestSizeAtMost: sizeAtMost counts a value and its elements, nested
// ones included, and stops at limit+1.
func TestSizeAtMost(t *testing.T) {
	bit := &eval.Type{Kind: eval.KindEnum, Name: "BIT",
		Literals: []string{"'0'", "'1'"},
		Range:    eval.Range{Left: 0, Right: 1, Dir: eval.To}}
	vec := func(n int) eval.Value {
		return eval.Value{E: make([]eval.Value, n)}
	}
	nested := eval.Value{E: []eval.Value{vec(3), vec(3), vec(3)}}
	cases := []struct {
		v           eval.Value
		limit, want int
	}{
		{eval.Enum(bit, 1), 0, 1},
		{eval.Enum(bit, 1), 5, 1},
		{vec(4), 5, 5},
		{vec(4), 4, 5},
		{vec(4), 3, 4},
		{vec(4), 0, 1},
		{nested, 13, 13},
		{nested, 12, 13},
		{nested, 7, 8},
		{nested, 1, 2},
	}
	for _, c := range cases {
		if got := sizeAtMost(c.v, c.limit); got != c.want {
			t.Errorf("sizeAtMost(%d elements, %d) = %d, want %d",
				len(c.v.E), c.limit, got, c.want)
		}
	}
}

// TestBuiltinClearPslState (#111): ClearPslState drops a directive's
// obligations but not the samples of its built-in calls, which are
// values the design had: stable(a) fails at 25 ns (a rose at 20 ns)
// and, after a clear at 26 ns, holds at 35 ns because a was '1' at the
// 25 ns tick too.
func TestBuiltinClearPslState(t *testing.T) {
	src := strings.NewReplacer("TYPE", "bit",
		"DIR", "assert always (stable(a));",
		"wait for 100 ns;",
		"wait for 26 ns; std.env.ClearPslState; wait for 74 ns;").
		Replace(builtinSrc)
	cfg := config.Defaults()
	cfg.Standard = "2019"
	r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
		Files: []sema.Source{{Path: "s.vhd", Text: []byte(src)}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("analysis: %s %s", d.Code, d.Message)
		}
	}
	d, ds := elab.Elaborate(r, elab.Options{Top: "s",
		Code: Wrap(elab.Interpreter(elab.InterpOptions{}))})
	if d == nil || len(ds) > 0 {
		t.Fatalf("elaborate: %v", ds)
	}
	rec := trace.NewRecorder(nil)
	k, err := sim.New(d.Model, sim.Options{Writer: rec,
		StopLevel: trace.Failure})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, e := range tr.Events {
		if e.Kind == trace.Assert {
			got = append(got, int64(e.Time)/ns)
		}
	}
	if !slices.Equal(got, []int64{25}) {
		t.Fatalf("failures at %v ns, want [25]", got)
	}
}

// TestBuiltinSamples (#111): on random sample sequences, every
// built-in's value at each tick agrees with a reference that indexes
// the whole sequence (sample 0 is the initial value; before the n-th
// sample prev is the default value), also for a runner state cloned
// at a random tick and then fed another sequence.
func TestBuiltinSamples(t *testing.T) {
	ul := &eval.Type{Kind: eval.KindEnum, Name: "STD_ULOGIC",
		Literals: []string{"'U'", "'X'", "'0'", "'1'", "'Z'", "'W'",
			"'L'", "'H'", "'-'"}, Range: eval.Range{Left: 0, Right: 8,
			Dir: eval.To}}
	vec := &eval.Type{Kind: eval.KindArray, Name: "STD_ULOGIC_VECTOR",
		Elem: ul}
	r := &rng{s: env.SplitMix64{State: 111}}
	scalar := func() eval.Value { return eval.Enum(ul, int64(r.n(9))) }
	vector := func() eval.Value {
		e := make([]eval.Value, 1+r.n(3))
		for i := range e {
			e[i] = scalar()
		}
		return eval.Array(vec,
			[]eval.Range{{Left: 0, Right: int64(len(e) - 1),
				Dir: eval.To}}, e)
	}
	kinds := []bKind{bPrev, bStable, bRose, bFell, bOnehot, bOnehot0,
		bIsunknown}
	for iter := range 300 {
		bi := &builtin{kind: kinds[r.n(len(kinds))]}
		value := vector
		switch bi.kind {
		case bPrev:
			bi.depth = 1 + r.n(5)
		case bStable:
			bi.depth = 1
		case bRose, bFell:
			bi.depth, value = 1, scalar
		}
		run := func(h *samples, ws []eval.Value, from int) {
			for k := from; k < len(ws); k++ {
				got, err := bi.value(ws[k], h)
				if err != nil {
					t.Fatal(err)
				}
				if want := refBuiltin(bi, ws, k); !got.Equal(want) {
					t.Fatalf("iter %d: kind %d depth %d at %d: %v, "+
						"want %v", iter, bi.kind, bi.depth, k, got, want)
				}
				h.push(ws[k], bi.depth)
			}
		}
		n := 1 + r.n(20)
		ws := make([]eval.Value, n)
		for i := range ws {
			ws[i] = value()
		}
		cut := r.n(n)
		var h samples
		run(&h, ws[:cut], 0)
		c := h.clone()
		run(&h, ws, cut)
		other := slices.Clone(ws[:cut])
		for len(other) < n {
			other = append(other, value())
		}
		run(&c, other, cut)
	}
}

// refBuiltin is the value of bi at sample k of ws (ws[0] is the
// initial value), read from the whole sequence.
func refBuiltin(bi *builtin, ws []eval.Value, k int) eval.Value {
	at := func(j int) eval.Value {
		if j >= 0 {
			return ws[j]
		}
		v, err := defaultLike(ws[k])
		if err != nil {
			panic(err)
		}
		return v
	}
	one := func(v eval.Value) bool {
		s := v.T.Literals[v.I]
		return s == "'1'" || s == "'H'"
	}
	count := func(v eval.Value) (ones, unknown int) {
		for _, e := range elements(v) {
			switch e.T.Literals[e.I] {
			case "'1'", "'H'":
				ones++
			case "'0'", "'L'":
			default:
				unknown++
			}
		}
		return ones, unknown
	}
	cur := ws[k]
	switch bi.kind {
	case bPrev:
		return at(k - bi.depth)
	case bStable:
		return eval.Bool(cur.Equal(at(k - 1)))
	case bRose:
		return eval.Bool(one(cur) && !one(at(k-1)))
	case bFell:
		return eval.Bool(!one(cur) && one(at(k-1)))
	}
	ones, unknown := count(cur)
	switch bi.kind {
	case bOnehot:
		return eval.Bool(ones == 1)
	case bOnehot0:
		return eval.Bool(ones <= 1)
	}
	return eval.Bool(unknown > 0)
}
