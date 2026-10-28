package trace_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
	"github.com/connerohnesorge/gohard/v2/internal/trace/tracetest"
)

var update = flag.Bool("update", false, "rewrite golden files")

// golden compares got with testdata/name, or rewrites it with -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := "testdata/" + name
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, a, b, ok := trace.FirstDiff(want, got); ok {
		t.Fatalf("%s differs at line %d:\nwant %q\n got %q", path, n, a,
			b)
	}
}

func TestSampleGolden(t *testing.T) {
	golden(t, "sample.trace", trace.Encode(tracetest.Sample()))
}

func TestDumpScopeGolden(t *testing.T) {
	tr := tracetest.MustRecord([]string{"tb.dut"}, tracetest.Feed)
	golden(t, "sample-dut.trace", trace.Encode(tr))
}

func record(t *testing.T, dump []string, s *tracetest.Stream) *trace.Trace {
	t.Helper()
	r := trace.NewRecorder(dump)
	if err := s.Feed(r); err != nil {
		t.Fatal(err)
	}
	tr, err := r.Trace()
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// TestRoundTrip: Decode(Encode(t)) encodes to the same bytes and gives
// the same trace, for the sample and for random traces.
func TestRoundTrip(t *testing.T) {
	traces := []*trace.Trace{tracetest.Sample()}
	for seed := range uint64(300) {
		traces = append(traces, record(t, nil,
			tracetest.Random(seed, 8, 12)))
	}
	for i, tr := range traces {
		b := trace.Encode(tr)
		back, err := trace.Decode(b)
		if err != nil {
			t.Fatalf("trace %d: %v\n%s", i, err, b)
		}
		if b2 := trace.Encode(back); !bytes.Equal(b, b2) {
			n, x, y, _ := trace.FirstDiff(b, b2)
			t.Fatalf("trace %d: line %d: %q != %q", i, n, x, y)
		}
		if len(back.Changes) != len(tr.Changes) ||
			len(back.Events) != len(tr.Events) {
			t.Fatalf("trace %d: entry counts differ", i)
		}
		for k := range tr.Changes {
			a, c := tr.Changes[k], back.Changes[k]
			if a.Time != c.Time || a.Delta != c.Delta ||
				a.Signal != c.Signal || a.Process != c.Process ||
				a.Modeled != c.Modeled || !a.Value.Equal(c.Value) {
				t.Fatalf("trace %d: change %d: %+v != %+v", i, k, a, c)
			}
		}
		if !slices.Equal(tr.Events, back.Events) {
			t.Fatalf("trace %d: events differ", i)
		}
	}
}

// permuteDeltas reorders the changes and events of every delta of s
// with seed: the in-delta order a different process order gives.
func permuteDeltas(s *tracetest.Stream, seed uint64) *tracetest.Stream {
	out := *s
	out.Changes, out.Events, out.Order = nil, nil, nil
	type item struct {
		ev bool
		c  trace.Change
		e  trace.Event
	}
	var group []item
	flush := func(step uint64) {
		// Changes and events of one delta: shuffle each kind, keep
		// changes first only where the stream had them first is not
		// required; a Writer must accept any in-delta order.
		perm := env.Shuffle(seed, step, len(group))
		var evs []trace.Event
		byProc := map[int][]trace.Event{}
		for _, it := range group {
			if it.ev {
				byProc[it.e.Process] = append(byProc[it.e.Process], it.e)
			}
		}
		// Events of one process keep their order; processes interleave
		// in a seed-dependent order.
		procs := []int{}
		for _, it := range group {
			if it.ev && !slices.Contains(procs, it.e.Process) {
				procs = append(procs, it.e.Process)
			}
		}
		for _, k := range env.Shuffle(seed, step+1000, len(procs)) {
			evs = append(evs, byProc[procs[k]]...)
		}
		for _, k := range perm {
			if !group[k].ev {
				out.Changes = append(out.Changes, group[k].c)
				out.Order = append(out.Order, false)
			}
		}
		for _, e := range evs {
			out.Events = append(out.Events, e)
			out.Order = append(out.Order, true)
		}
		group = group[:0]
	}
	ci, ei := 0, 0
	var at trace.Time
	var delta uint32
	step := uint64(0)
	for _, ev := range s.Order {
		var it item
		if ev {
			it = item{ev: true, e: s.Events[ei]}
			ei++
		} else {
			it = item{c: s.Changes[ci]}
			ci++
		}
		t, d := it.c.Time, it.c.Delta
		if ev {
			t, d = it.e.Time, it.e.Delta
		}
		if len(group) > 0 && (t != at || d != delta) {
			flush(step)
			step++
		}
		at, delta = t, d
		group = append(group, it)
	}
	flush(step)
	return &out
}

// TestSeedIndependent: a race-free run (the same changes per delta)
// gives byte-identical traces whatever order the processes ran in, and
// entries within a delta are sorted by signal path.
func TestSeedIndependent(t *testing.T) {
	for seed := range uint64(100) {
		s := tracetest.Random(seed, 8, 10)
		want := trace.Encode(record(t, nil, s))
		for k := range uint64(4) {
			// Another seed runs the processes in another order; the
			// seed itself must not reach the bytes either (#36).
			p := permuteDeltas(s, k+1)
			p.Header.Seed = seed + 1000*(k+1)
			got := trace.Encode(record(t, nil, p))
			if n, a, b, ok := trace.FirstDiff(want, got); ok {
				t.Fatalf("seed %d order %d: line %d: %q != %q", seed, k,
					n, a, b)
			}
		}
		tr := record(t, nil, s)
		for i := 1; i < len(tr.Changes); i++ {
			a, b := tr.Changes[i-1], tr.Changes[i]
			if a.Time == b.Time && a.Delta == b.Delta &&
				tr.Signals[a.Signal].Path >= tr.Signals[b.Signal].Path {
				t.Fatalf("seed %d: %s before %s in one delta", seed,
					tr.Signals[a.Signal].Path, tr.Signals[b.Signal].Path)
			}
		}
	}
}

func TestInScope(t *testing.T) {
	cases := []struct {
		path   string
		scopes []string
		want   bool
	}{
		{"tb.dut.q", nil, true},
		{"tb.dut.q", []string{"tb.dut"}, true},
		{"tb.dut", []string{"tb.dut"}, true},
		{"tb.dut2.q", []string{"tb.dut"}, false},
		{"tb.du.q", []string{"tb.dut"}, false},
		{"tb.q", []string{"tb.dut"}, false},
		{"tb.mem.q", []string{"tb.dut", "tb.mem"}, true},
		{"tb.dut.q", []string{"tb"}, true},
	}
	for _, c := range cases {
		if got := trace.InScope(c.path, c.scopes); got != c.want {
			t.Errorf("InScope(%q, %q) = %v", c.path, c.scopes, got)
		}
	}
}

func TestDumpScope(t *testing.T) {
	tr := tracetest.MustRecord([]string{"tb.dut", "tb.gen(0)"},
		tracetest.Feed)
	for _, s := range tr.Signals {
		if !strings.HasPrefix(s.Path, "tb.dut.") &&
			!strings.HasPrefix(s.Path, "tb.gen(0).") {
			t.Errorf("signal %s outside dump_scope", s.Path)
		}
	}
	var scopes []string
	for _, s := range tr.Scopes {
		scopes = append(scopes, s.Path)
	}
	want := []string{"tb", "tb.dut", "tb.dut.bus", "tb.gen(0)"}
	if !slices.Equal(scopes, want) {
		t.Errorf("scopes = %q, want %q", scopes, want)
	}
	for _, ty := range tr.Types {
		if ty.Name == "unused_t" || ty.Name == "integer" {
			t.Errorf("type %s kept without a signal", ty.Name)
		}
	}
	full := tracetest.Sample()
	if len(tr.Events) != len(full.Events) {
		t.Errorf("dump_scope dropped events: %d of %d", len(tr.Events),
			len(full.Events))
	}
	// A prefix that is not a whole path component selects nothing.
	none := tracetest.MustRecord([]string{"tb.du"}, tracetest.Feed)
	if len(none.Signals) != 0 || len(none.Changes) != 0 {
		t.Errorf("tb.du selected %d signals", len(none.Signals))
	}
	// Every change of a kept signal matches the full trace.
	for _, s := range tr.Signals {
		i, _ := tr.Lookup(s.Path)
		j, _ := full.Lookup(s.Path)
		a, b := tr.ChangesOf(i), full.ChangesOf(j)
		if len(a) != len(b) {
			t.Fatalf("%s: %d changes, want %d", s.Path, len(a), len(b))
		}
		for k := range a {
			if !a[k].Value.Equal(b[k].Value) || a[k].Time != b[k].Time {
				t.Fatalf("%s: change %d differs", s.Path, k)
			}
		}
	}
}

func TestModeled(t *testing.T) {
	tr := tracetest.Sample()
	mem, _ := tr.Lookup("tb.mem.dout")
	for _, c := range tr.Changes {
		modeled := c.Process > 0 && tr.Processes[c.Process-1].Modeled
		if c.Modeled != modeled {
			t.Errorf("%s at %d+%d: modeled %v, want %v",
				tr.Signals[c.Signal].Path, c.Time, c.Delta, c.Modeled,
				modeled)
		}
		if c.Signal == mem && c.Process > 0 && !c.Modeled {
			t.Errorf("model output not marked modeled")
		}
	}
	n := 0
	for _, c := range tr.Changes {
		if c.Modeled {
			n++
		}
	}
	if n != 4 {
		t.Errorf("%d modeled changes, want 4", n)
	}
	// A modeled change must say so in the text form.
	b := trace.Encode(tr)
	bad := bytes.Replace(b, []byte(" p3 modeled\n"), []byte(" p3\n"), 1)
	if _, err := trace.Decode(bad); err == nil ||
		!strings.Contains(err.Error(), "modeled") {
		t.Errorf("unmarked modeled change accepted: %v", err)
	}
}

func TestEvents(t *testing.T) {
	tr := tracetest.Sample()
	var got []string
	for _, e := range tr.Events {
		p := "-"
		if e.Process > 0 {
			p = tr.Processes[e.Process-1].Path
		}
		got = append(got, fmt.Sprintf("%d+%d %s %s %s:%d %s %q", e.Time,
			e.Delta, e.Kind, e.Severity, e.File, e.Line, p, e.Message))
	}
	want := []string{
		`0+0 report note tb.vhd:30 tb.stim "start"`,
		`20000000+2 assert error rtl/counter.vhd:18 tb.dut.p_count ` +
			`"count_q /= \"00000010\" after reset"`,
		`20000000+2 cover-hit - tb.vhd:51 tb.stim "count reached 2"`,
		`20000000+2 report warning tb.vhd:52 tb.stim "second edge"`,
		`40000000+0 cover-miss - tb.vhd:60 - "overflow never seen"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"),
			strings.Join(want, "\n"))
	}
}

// TestGlitchKept: the native trace is lossless, so a value that changes
// and changes back within one time step keeps both changes, while
// Settle drops it.
func TestGlitchKept(t *testing.T) {
	tr := tracetest.Sample()
	i, _ := tr.Lookup("tb.dut.bus.valid")
	cs := tr.ChangesOf(i)
	if len(cs) != 3 || cs[1].Delta != 2 || cs[2].Delta != 3 {
		t.Fatalf("valid changes = %+v", cs)
	}
	for _, st := range trace.Settle(tr) {
		for _, c := range st.Changes {
			if c.Signal == i && st.Time != 0 {
				t.Errorf("glitch at %d in settled values", st.Time)
			}
		}
	}
}

func TestSettleMatchesValueAt(t *testing.T) {
	for seed := range uint64(100) {
		tr := record(t, nil, tracetest.Random(seed, 6, 10))
		cur := make([]*trace.Value, len(tr.Signals))
		for _, st := range trace.Settle(tr) {
			for _, c := range st.Changes {
				v := c.Value
				cur[c.Signal] = &v
			}
			for i, s := range tr.Signals {
				v, _, ok := tr.ValueAt(s.Path, st.Time)
				if ok != (cur[i] != nil) || ok && !v.Equal(*cur[i]) {
					t.Fatalf("seed %d: %s at %d: settled and ValueAt "+
						"disagree", seed, s.Path, st.Time)
				}
			}
		}
	}
}

// naiveAt scans every change: the reference model of ValueAtDelta.
func naiveAt(tr *trace.Trace, sig int, at trace.Time, delta uint32) (
	trace.Value, bool,
) {
	var v trace.Value
	ok := false
	for _, c := range tr.Changes {
		if c.Signal == sig && (c.Time < at ||
			c.Time == at && c.Delta <= delta) {
			v, ok = c.Value, true
		}
	}
	return v, ok
}

func TestValueLookupProperty(t *testing.T) {
	traces := []*trace.Trace{tracetest.Sample()}
	for seed := range uint64(200) {
		traces = append(traces, record(t, nil,
			tracetest.Random(seed+1000, 8, 15)))
	}
	r := &env.SplitMix64{State: 99}
	for n, tr := range traces {
		for range 200 {
			sig := env.Intn(r, len(tr.Signals))
			at := trace.Time(env.Intn(r, int(tr.Header.End)+3))
			delta := uint32(env.Intn(r, 5))
			if env.Intn(r, 4) == 0 {
				delta = ^uint32(0)
			}
			want, wok := naiveAt(tr, sig, at, delta)
			got, c, ok := tr.ValueAtDelta(tr.Signals[sig].Path, at, delta)
			if ok != wok || ok && (!got.Equal(want) ||
				!c.Value.Equal(want) || c.Signal != sig) {
				t.Fatalf("trace %d: %s at %d+%d: got %v %v, want %v %v",
					n, tr.Signals[sig].Path, at, delta, got, ok, want, wok)
			}
		}
	}
	tr := tracetest.Sample()
	if _, _, ok := tr.ValueAt("tb.nope", 0); ok {
		t.Error("unknown signal has a value")
	}
	v, c, ok := tr.ValueAt("tb.dut.count_q", 25*tracetest.NS)
	if !ok || tr.FormatValue(tr.SignalType(c.Signal), v) != "00000010" ||
		c.Time != 20*tracetest.NS || c.Delta != 2 {
		t.Errorf("count_q at 25 ns = %v %+v", v, c)
	}
	v, _, _ = tr.ValueAtDelta("tb.dut.bus.valid", 0, 2)
	if v.Int != 1 {
		t.Errorf("valid at 0+2 = %v, want true", v)
	}
	v, _, _ = tr.ValueAt("tb.dut.bus.valid", 0)
	if v.Int != 0 {
		t.Errorf("valid settled at 0 = %v, want false", v)
	}
}

func TestRecorderErrors(t *testing.T) {
	d := trace.Design{
		Types: []trace.Type{{Name: "std_logic", Kind: trace.Logic},
			{Name: "small", Kind: trace.Integer, Low: 0, High: 3}},
		Signals: []trace.Signal{
			{Path: "tb.a", Type: "std_logic", Dir: trace.Internal},
			{Path: "tb.n", Type: "small", Dir: trace.Internal},
		},
	}
	h := trace.Header{Testbench: "tb"}
	ch := func(at trace.Time, delta uint32, sig int, v int64) trace.Change {
		return trace.Change{Time: at, Delta: delta, Signal: sig,
			Value: trace.Int(v)}
	}
	cases := []struct {
		name string
		run  func(w *trace.Recorder) error
		want string
	}{
		{"before begin", func(w *trace.Recorder) error {
			return w.Change(ch(0, 0, 0, 0))
		}, "before Begin"},
		{"time backwards", func(w *trace.Recorder) error {
			w.Begin(h, d)
			w.Change(ch(5, 0, 0, 0))
			return w.Change(ch(4, 0, 1, 0))
		}, "after 5+0"},
		{"delta backwards", func(w *trace.Recorder) error {
			w.Begin(h, d)
			w.Change(ch(5, 2, 0, 0))
			return w.Change(ch(5, 1, 1, 0))
		}, "after 5+2"},
		{"out of range", func(w *trace.Recorder) error {
			w.Begin(h, d)
			return w.Change(ch(0, 0, 1, 4))
		}, "out of range"},
		{"bad logic", func(w *trace.Recorder) error {
			w.Begin(h, d)
			return w.Change(ch(0, 0, 0, 9))
		}, "std_ulogic position"},
		{"no signal", func(w *trace.Recorder) error {
			w.Begin(h, d)
			return w.Change(ch(0, 0, 2, 0))
		}, "no signal"},
		{"no process", func(w *trace.Recorder) error {
			w.Begin(h, d)
			c := ch(0, 0, 0, 0)
			c.Process = 1
			return w.Change(c)
		}, "no process"},
		{"after end", func(w *trace.Recorder) error {
			w.Begin(h, d)
			w.End(3)
			return w.Change(ch(3, 0, 0, 0))
		}, "after End"},
		{"end early", func(w *trace.Recorder) error {
			w.Begin(h, d)
			w.Change(ch(3, 0, 0, 0))
			return w.End(2)
		}, "End at 2"},
		{"twice in a delta", func(w *trace.Recorder) error {
			w.Begin(h, d)
			w.Change(ch(0, 0, 0, 0))
			w.Change(ch(0, 0, 0, 1))
			w.End(0)
			_, err := w.Trace()
			return err
		}, "changes twice"},
		{"duplicate signal", func(w *trace.Recorder) error {
			d2 := d
			d2.Signals = append(slices.Clone(d.Signals), d.Signals[0])
			return w.Begin(h, d2)
		}, "duplicate signal"},
		{"unknown type", func(w *trace.Recorder) error {
			d2 := d
			d2.Types = d.Types[:1]
			return w.Begin(h, d2)
		}, "unknown type"},
		{"sticky", func(w *trace.Recorder) error {
			w.Change(ch(0, 0, 0, 0))
			w.Begin(h, d)
			return w.End(0)
		}, "before Begin"},
	}
	for _, c := range cases {
		err := c.run(trace.NewRecorder(nil))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	r := trace.NewRecorder(nil)
	r.Begin(h, d)
	if _, err := r.Trace(); err == nil {
		t.Error("Trace before End succeeded")
	}
}

// TestNoOpDropped: a change to the value a signal already has is not a
// change.
func TestNoOpDropped(t *testing.T) {
	d := trace.Design{
		Types:   []trace.Type{{Name: "std_logic", Kind: trace.Logic}},
		Signals: []trace.Signal{{Path: "tb.a", Type: "std_logic", Dir: 1}},
	}
	r := trace.NewRecorder(nil)
	r.Begin(trace.Header{Testbench: "tb"}, d)
	for k, c := range "0011010" {
		r.Change(trace.Change{Time: trace.Time(k), Signal: 0,
			Value: trace.LogicValue(byte(c))})
	}
	r.End(10)
	tr, err := r.Trace()
	if err != nil {
		t.Fatal(err)
	}
	var times []trace.Time
	for _, c := range tr.Changes {
		times = append(times, c.Time)
	}
	if !slices.Equal(times, []trace.Time{0, 2, 4, 5, 6}) {
		t.Errorf("change times = %v", times)
	}
}

func TestDecodeErrors(t *testing.T) {
	good := string(trace.Encode(tracetest.Sample()))
	cases := []struct {
		name, from, to, want string
	}{
		{"magic", "gohard-trace 1", "gohard-trace 2", "line 1"},
		{"header", "end 40000000", "ends 40000000", "line 3: want end"},
		// The seed is not part of the canonical form (#36).
		{"seed line", "testbench tb\n", "testbench tb\nseed 7\n",
			"line 3: want end"},
		{"plus sign", "end 40000000", "end +40000000",
			"line 3: bad number +40000000"},
		{"leading zero", "end 40000000", "end 040000000",
			"line 3: not in canonical"},
		{"type order",
			"type bit enum '0' '1'\ntype boolean enum false true",
			"type boolean enum false true\ntype bit enum '0' '1'",
			"types not sorted"},
		{"double space", "0 0 tb.clk U -", "0 0  tb.clk U -", "line 40"},
		{"unknown signal", "0 0 tb.clk U -", "0 0 tb.clock U -",
			"line 40: unknown signal tb.clock"},
		{"bad logic", "0 0 tb.clk U -", "0 0 tb.clk u -",
			"line 40: bad std_ulogic"},
		{"unsorted", "0 0 tb.clk U -\n0 0 tb.dut.bus.addr XXXX -",
			"0 0 tb.dut.bus.addr XXXX -\n0 0 tb.clk U -",
			"changes not sorted"},
		{"no-op", "15000000 0 tb.clk 0 p1", "15000000 0 tb.clk 1 p1",
			"does not change"},
		{"literal", "0 0 tb.dut.state idle -", "0 0 tb.dut.state busy -",
			"busy is not a literal"},
		{"vector length", "0 0 tb.mem.dout UUUU -",
			"0 0 tb.mem.dout UUU -", "3 elements for a range of 4"},
		{"process", "0 1 tb.clk 0 p1", "0 1 tb.clk 0 p9", "no process 9"},
		{"section order", "process 1 tb.clkgen\n",
			"process 1 tb.clkgen\ntype zz real\n", "after a later section"},
		{"event severity", "! cover-miss - ", "! cover-miss note ",
			"has no severity"},
		{"after end", "end 40000000", "end 30000000", "after the end"},
		{"trailing", "0 0 tb.clk U -", "0 0 tb.clk U - x",
			"line 40: unexpected x"},
		{"real", "0 0 tb.dut.ratio 0.5 -", "0 0 tb.dut.ratio 5e-1 -",
			"not in canonical"},
	}
	for _, c := range cases {
		if !strings.Contains(good, c.from) {
			t.Fatalf("%s: %q not in the sample", c.name, c.from)
		}
		bad := strings.Replace(good, c.from, c.to, 1)
		_, err := trace.Decode([]byte(bad))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if _, err := trace.Decode([]byte(good[:len(good)-1])); err == nil {
		t.Error("missing final newline accepted")
	}
	if _, err := trace.Decode(nil); err == nil {
		t.Error("empty input accepted")
	}
}

func TestQuotedTokens(t *testing.T) {
	d := trace.Design{
		Types: []trace.Type{
			{Name: "character", Kind: trace.Enum,
				Literals: []string{"' '", "'\"'", "nul"}},
			{Name: "str", Kind: trace.Array, Elem: "character"},
			{Name: "std_logic", Kind: trace.Logic},
			{Name: "std_logic_vector", Kind: trace.Array,
				Elem: "std_logic"},
		},
		Signals: []trace.Signal{
			{Path: `tb.\my sig\`, Type: "character", Dir: trace.Internal},
			{Path: `tb.\a.b\`, Type: "str", Dir: trace.Internal,
				Left: 1, Right: 2},
			{Path: "tb.null", Type: "std_logic_vector",
				Dir: trace.Internal, Left: 0, Right: -1},
		},
	}
	r := trace.NewRecorder(nil)
	r.Begin(trace.Header{Testbench: `\T B\`, Seed: 1}, d)
	r.Change(trace.Change{Signal: 0, Value: trace.Int(0)})
	r.Change(trace.Change{Signal: 1,
		Value: trace.Elems(trace.Int(1), trace.Int(2))})
	r.Change(trace.Change{Signal: 2, Value: trace.Vector("")})
	r.Event(trace.Event{Kind: trace.Report, Severity: trace.Failure,
		File: "my tb.vhd", Line: 3, Message: "tab\there é"})
	r.End(0)
	tr, err := r.Trace()
	if err != nil {
		t.Fatal(err)
	}
	b := trace.Encode(tr)
	golden(t, "quoted.trace", b)
	if _, err := trace.Decode(b); err != nil {
		t.Fatal(err)
	}
	if got := trace.SplitPath(`tb.\a.b\.q`); !slices.Equal(got,
		[]string{"tb", `\a.b\`, "q"}) {
		t.Errorf("SplitPath = %q", got)
	}
	if got := trace.SplitPath(`tb.\a\\.b\.q`); !slices.Equal(got,
		[]string{"tb", `\a\\.b\`, "q"}) {
		t.Errorf("SplitPath = %q", got)
	}
}

func TestFileName(t *testing.T) {
	cases := map[string]string{
		"tb":        ".gohard/traces/tb-7.trace",
		`\Foo\`:     ".gohard/traces/%5C%46oo%5C-7.trace",
		"work.tb_a": ".gohard/traces/work.tb_a-7.trace",
	}
	for tb, want := range cases {
		if got := trace.FileName(tb, 7); got != want {
			t.Errorf("FileName(%q) = %q, want %q", tb, got, want)
		}
	}
}

func TestWriteRead(t *testing.T) {
	sim := simenv.New(simenv.Options{})
	fsys := sim.Env().FS
	tr := tracetest.Sample()
	name, err := trace.Write(fsys, "/work", tr)
	if err != nil {
		t.Fatal(err)
	}
	if name != ".gohard/traces/tb-7.trace" {
		t.Errorf("name = %q", name)
	}
	back, err := trace.Read(fsys, "/work/"+name)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(trace.Encode(back), trace.Encode(tr)) {
		t.Error("read trace differs")
	}
	ents, err := fsys.ReadDir("/work/.gohard/traces")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != "tb-7.trace" {
		t.Errorf("trace folder holds %v (temp file left behind?)", ents)
	}
	// A failed rename leaves no temp file and no trace.
	sim2 := simenv.New(simenv.Options{})
	if _, err := trace.Write(failRename{sim2.Env().FS}, "/work",
		tr); err == nil {
		t.Fatal("write succeeded despite a failed rename")
	}
	ents, _ = sim2.FS.ReadDir("/work/.gohard/traces")
	if len(ents) != 0 {
		t.Errorf("left %v after a failed rename", ents)
	}
}

type failRename struct{ env.FS }

func (failRename) Rename(string, string) error {
	return fmt.Errorf("disk full")
}
