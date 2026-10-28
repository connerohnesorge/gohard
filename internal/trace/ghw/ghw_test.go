package ghw

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/trace"
	"github.com/connerohnesorge/gohard/v2/internal/trace/tracetest"
)

var update = flag.Bool("update", false, "rewrite golden files")

func randomTrace(t *testing.T, seed uint64) *trace.Trace {
	t.Helper()
	r := trace.NewRecorder(nil)
	if err := tracetest.Random(seed, 8, 12).Feed(r); err != nil {
		t.Fatal(err)
	}
	tr, err := r.Trace()
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// shapes is a trace with the shapes the sample lacks: nested and
// mixed-mode records, record scopes that stay blocks, generates of
// every kind, a package, an extended identifier, 64-bit integers,
// special reals, a null range and a signal with no initial value.
func shapes(t *testing.T) *trace.Trace {
	t.Helper()
	types := append(tracetest.Types(),
		trace.Type{Name: "big", Kind: trace.Integer, Low: math.MinInt64,
			High: math.MaxInt64},
		trace.Type{Name: "tiny", Kind: trace.Integer, Low: -3, High: 3},
		trace.Type{Name: "std_ulogic", Kind: trace.Logic},
		trace.Type{Name: "ulv", Kind: trace.Array, Elem: "std_ulogic"},
		trace.Type{Name: "reals", Kind: trace.Array, Elem: "real"},
	)
	sig := func(path, typ string, dir trace.Dir) trace.Signal {
		return trace.Signal{Path: path, Type: typ, Dir: dir}
	}
	vec := func(path, typ string, dir trace.Dir, l, r int64,
		down bool,
	) trace.Signal {
		return trace.Signal{Path: path, Type: typ, Dir: dir, Left: l,
			Right: r, Downto: down}
	}
	d := trace.Design{
		Types: types,
		Scopes: []trace.Scope{
			{Path: "tb", Kind: trace.Instance, Unit: "tb(sim)"},
			{Path: "tb.r", Kind: trace.Record, Unit: "pair_t"},
			{Path: "tb.r.n", Kind: trace.Record, Unit: "inner_t"},
			{Path: "tb.m", Kind: trace.Record, Unit: "pair_t"},
			{Path: "tb.m2", Kind: trace.Record, Unit: "pair_t"},
			{Path: "tb.m3", Kind: trace.Record, Unit: "pair_t"},
			{Path: "tb.q", Kind: trace.Record, Unit: "q_t"},
			{Path: "tb.e", Kind: trace.Record, Unit: "e_t"},
			{Path: "tb.w", Kind: trace.Record, Unit: "w_t"},
			{Path: "tb.g(idle)", Kind: trace.ForGenerate},
			{Path: "tb.h(-3)", Kind: trace.ForGenerate},
			{Path: "tb.k(007)", Kind: trace.ForGenerate},
			{Path: "tb.c.alt", Kind: trace.CaseGenerate},
			{Path: "tb.i", Kind: trace.IfGenerate},
			{Path: "tb.blk", Kind: trace.Block},
			{Path: "pkg", Kind: trace.Package},
		},
		Signals: []trace.Signal{
			sig("tb.r.a", "std_logic", trace.In),
			vec("tb.r.b", "std_logic_vector", trace.In, 3, 0, true),
			sig("tb.r.n.x", "tiny", trace.In),
			sig("tb.r.n.y", "state_t", trace.In),
			sig("tb.m.a", "std_logic", trace.Out),
			vec("tb.m.b", "std_logic_vector", trace.In, 3, 0, true),
			sig("tb.m2.a", "std_logic", trace.Internal),
			vec("tb.m2.b", "std_logic_vector", trace.Internal, 0, 3,
				false),
			sig("tb.m3.a", "std_logic", trace.Out),
			vec("tb.m3.b", "std_logic_vector", trace.Out, 3, 0, true),
			sig("tb.q.a", "bit", trace.Internal),
			sig("tb.w.x.y", "boolean", trace.Internal),
			sig("tb.g(idle).s", "std_ulogic", trace.Internal),
			sig("tb.h(-3).s", "std_ulogic", trace.Internal),
			sig("tb.k(007).s", "std_ulogic", trace.Internal),
			sig("tb.c.alt.s", "std_ulogic", trace.Internal),
			sig("tb.i.s", "time", trace.Buffer),
			sig("tb.blk.s", "natural", trace.Linkage),
			sig(`tb.\a.b\`, "std_logic", trace.Inout),
			sig("tb.big", "big", trace.Internal),
			sig("tb.f", "real", trace.Internal),
			vec("tb.nul", "ulv", trace.Internal, 0, -1, false),
			vec("tb.rs", "reals", trace.Internal, 1, 2, false),
			sig("tb.late", "integer", trace.Internal),
			sig("pkg.s", "std_ulogic", trace.Internal),
		},
		Processes: []trace.Process{
			{Path: "tb.q.p"},
			{Path: "tb.stim"},
			{Path: "tb.blk.p", Modeled: true},
		},
	}
	idx := map[string]int{}
	for i, s := range d.Signals {
		idx[s.Path] = i
	}
	r := trace.NewRecorder(nil)
	if err := r.Begin(trace.Header{Testbench: "tb"}, d); err != nil {
		t.Fatal(err)
	}
	ch := func(at trace.Time, path string, v trace.Value) {
		t.Helper()
		i, ok := idx[path]
		if !ok {
			t.Fatalf("no signal %s", path)
		}
		if err := r.Change(trace.Change{Time: at, Signal: i, Value: v,
			Process: 2}); err != nil {
			t.Fatal(err)
		}
	}
	L, V, I, F := trace.LogicValue, trace.Vector, trace.Int, trace.Float
	for _, s := range d.Signals {
		if s.Path == "tb.late" {
			continue
		}
		var v trace.Value
		switch s.Type {
		case "std_logic", "std_ulogic":
			v = L('U')
		case "std_logic_vector", "ulv":
			v = V(strings.Repeat("U", s.Len()))
		case "reals":
			v = trace.Elems(F(0), F(0))
		case "big":
			v = I(math.MinInt64)
		case "tiny":
			v = I(-3)
		case "time":
			v = I(math.MinInt64)
		}
		ch(0, s.Path, v)
	}
	ch(5, "tb.late", I(-7))
	ch(5, "tb.big", I(math.MaxInt64))
	ch(5, "tb.f", F(math.NaN()))
	ch(5, "tb.rs", trace.Elems(F(math.Inf(1)), F(math.Copysign(0, -1))))
	ch(5, "tb.r.b", V("01ZW"))
	ch(5, "tb.m.a", L('H'))
	ch(5, "tb.r.n.y", I(2))
	ch(5, "tb.i.s", I(12345))
	ch(7, "tb.f", F(-1.5e300))
	ch(7, "tb.rs", trace.Elems(F(math.Inf(-1)), F(1e-310)))
	ch(7, "tb.r.n.x", I(3))
	ch(7, `tb.\a.b\`, L('-'))
	ch(7, "tb.q.a", I(1))
	ch(7, "tb.big", I(-1))
	if err := r.End(20); err != nil {
		t.Fatal(err)
	}
	tr, err := r.Trace()
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

type golden struct {
	name string
	tr   *trace.Trace
}

func corpusGoldens(t *testing.T) []golden {
	t.Helper()
	files, err := filepath.Glob("../../../testdata/corpus/*/golden/trace.txt")
	if err != nil || len(files) < 13 {
		t.Fatalf("%d corpus goldens, err %v", len(files), err)
	}
	var out []golden
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := trace.Decode(data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out = append(out, golden{
			filepath.Base(filepath.Dir(filepath.Dir(f))), tr})
	}
	return out
}

func TestSampleRoundTrip(t *testing.T) {
	f, _ := check(t, tracetest.Sample())
	got := dump(f, false)
	for _, want := range []string{
		"subtype std_logic is std_ulogic range 'U' to '-';\n",
		"type boolean is (false, true);  -- WKT:1\n",
		"type bit is ('0', '1');  -- WKT:2\n",
		"type std_ulogic is ('U', 'X', '0', '1', 'Z', 'W', 'L', 'H', " +
			"'-');  -- WKT:3\n",
		"subtype natural is natural range 0 to 2147483647;\n",
		"type std_logic_vector is array (integer range <>) of " +
			"std_logic;\n",
		"type bus_t is record\n  addr: std_logic_vector (3 downto 0);" +
			"\n  valid: boolean;\nend record;\n",
		"  us = 1000000000 fs;\nend units;\n",
		"signal /tb/dut/bus: bus_t: #3-#7\n",
		"port-out /tb/dut/count_q: std_logic_vector (7 downto 0): " +
			"#9-#16\n",
		"generate-for /tb/gen(0): (0)\n",
		"signal /tb/gen(0)/mode: state_array (1 to 2): #",
		"process /tb/mem/p_rd: \n",
		"instance /top2: \n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dump lacks %q:\n%s", want, got)
		}
	}
}

func TestShapesRoundTrip(t *testing.T) {
	f, _ := check(t, shapes(t))
	got := dump(f, false)
	for _, want := range []string{
		"port-in /tb/r: pair_t: #",
		"port-inout /tb/m: pair_t: #",
		"signal /tb/m2: pair_t: #",
		"port-out /tb/m3: pair_t: #",
		"type inner_t is record\n  x: tiny;\n  y: state_t;\nend record;\n",
		"block /tb/q: \n",
		"block /tb/e: \n",
		"block /tb/w: \n",
		"block /tb/g(idle): \n",
		"generate-for /tb/h(-3): (-3)\n",
		"block /tb/k(007): \n",
		"instance /tb/c: \n",
		"generate-if /tb/c/alt: \n",
		"package /pkg: \n",
		"port-inout /tb/\\a.b\\: std_logic: #",
		"signal /tb/nul: ulv (0 to -1):\n",
		"type big is range <> units\nend units;\n",
		"subtype big is big range -9223372036854775808 to " +
			"9223372036854775807;\n",
		"subtype real is real range -1.79769e+308 to 1.79769e+308;\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dump lacks %q:\n%s", want, got)
		}
	}
	// Four pair_t records: m and m3 share a type, m2 has another
	// range and r another field.
	if n := strings.Count(got, "type pair_t is record"); n != 3 {
		t.Errorf("%d pair_t record types, want 3", n)
	}
}

// TestRandomRoundTrip is the property: every generated trace comes
// back with its types, hierarchy and settled values.
func TestRandomRoundTrip(t *testing.T) {
	for seed := range uint64(300) {
		tr := randomTrace(t, seed)
		if _, _, err := roundTrip(tr); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

// TestCorpusGoldens: every corpus golden trace exports to GHW, the
// bytes are the same across exports, a text round trip and seeds, and
// the file reads back to the trace.
func TestCorpusGoldens(t *testing.T) {
	for _, g := range corpusGoldens(t) {
		_, a, err := roundTrip(g.tr)
		if err != nil {
			t.Fatalf("%s: %v", g.name, err)
		}
		back, err := trace.Decode(trace.Encode(g.tr))
		if err != nil {
			t.Fatal(err)
		}
		back.Header.Seed ^= 0x5eed
		b, _ := Exporter{}.Export(back)
		c, _ := Exporter{}.Export(g.tr)
		if !bytes.Equal(a, b) || !bytes.Equal(a, c) {
			t.Fatalf("%s: GHW export not stable", g.name)
		}
	}
}

// TestGolden pins the sample export byte for byte and by SHA-256.
func TestGolden(t *testing.T) {
	data, err := Exporter{}.Export(tracetest.Sample())
	if err != nil {
		t.Fatal(err)
	}
	sum := fmt.Sprintf("%x\n", sha256.Sum256(data))
	if *update {
		os.WriteFile("testdata/sample.ghw", data, 0o644)
		os.WriteFile("testdata/sample.ghw.sha256", []byte(sum), 0o644)
		return
	}
	want, err := os.ReadFile("testdata/sample.ghw")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, data) {
		t.Fatal("export differs from testdata/sample.ghw")
	}
	if h, _ := os.ReadFile("testdata/sample.ghw.sha256"); string(h) != sum {
		t.Fatalf("sha256 %s, want %s", sum, h)
	}
}

func TestStable(t *testing.T) {
	for seed := range uint64(40) {
		tr := randomTrace(t, seed)
		back, err := trace.Decode(trace.Encode(tr))
		if err != nil {
			t.Fatal(err)
		}
		back.Header.Seed = seed + 1
		a, _ := Exporter{}.Export(tr)
		b, _ := Exporter{}.Export(back)
		if !bytes.Equal(a, b) {
			t.Fatalf("seed %d: export not stable", seed)
		}
	}
}

func TestEmpty(t *testing.T) {
	for _, end := range []trace.Time{0, 5} {
		r := trace.NewRecorder(nil)
		r.Begin(trace.Header{Testbench: "tb"}, trace.Design{})
		r.End(end)
		tr, err := r.Trace()
		if err != nil {
			t.Fatal(err)
		}
		f, _ := check(t, tr)
		if f.nbasic != 0 || len(f.strs) != 1 || len(f.types) != 0 {
			t.Errorf("empty trace: %d signals, %d strings, %d types",
				f.nbasic, len(f.strs)-1, len(f.types))
		}
		if want := min(int(end), 1); len(f.cycles) != want {
			t.Errorf("end %d: %d cycles, want %d", end, len(f.cycles),
				want)
		}
	}
	// A signal without any change shows its first value throughout.
	r := trace.NewRecorder(nil)
	r.Begin(trace.Header{Testbench: "tb"}, trace.Design{
		Types: []trace.Type{{Name: "n", Kind: trace.Integer, Low: 2,
			High: 9}},
		Signals: []trace.Signal{{Path: "tb.a", Type: "n", Dir: 1}},
	})
	r.End(5)
	tr, _ := r.Trace()
	if f, _ := check(t, tr); f.snap[1].i != 2 || len(f.cycles) != 1 {
		t.Errorf("no changes: snapshot %v, %d cycles", f.snap[1],
			len(f.cycles))
	}
}

func TestUnwritable(t *testing.T) {
	lits := make([]string, 257)
	for i := range lits {
		lits[i] = fmt.Sprintf("l%d", i)
	}
	for _, c := range []struct {
		d    trace.Design
		want string
	}{
		{trace.Design{
			Types: []trace.Type{{Name: "e", Kind: trace.Enum,
				Literals: lits}},
			Signals: []trace.Signal{{Path: "tb.e", Type: "e", Dir: 1}},
		}, "257 literals"},
		{trace.Design{
			Types: []trace.Type{{Name: "std_logic", Kind: trace.Logic}},
			Signals: []trace.Signal{{Path: "tb.a\u0100",
				Type: "std_logic", Dir: 1}},
		}, "cannot hold"},
		{trace.Design{
			Types: []trace.Type{{Name: "std_logic", Kind: trace.Logic},
				{Name: "v", Kind: trace.Array, Elem: "std_logic"}},
			Signals: []trace.Signal{{Path: "tb.v", Type: "v", Dir: 1,
				Left: 1 << 40, Right: 1<<40 + 1}},
		}, "beyond 32 bits"},
	} {
		r := trace.NewRecorder(nil)
		if err := r.Begin(trace.Header{Testbench: "tb"}, c.d); err != nil {
			t.Fatal(err)
		}
		r.End(0)
		tr, err := r.Trace()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (Exporter{}).Export(tr); err == nil ||
			!strings.Contains(err.Error(), c.want) {
			t.Errorf("error %v, want %q", err, c.want)
		}
	}
}

func TestEncodings(t *testing.T) {
	for _, c := range []struct {
		v    int64
		want []byte
	}{
		{0, []byte{0}}, {63, []byte{63}}, {64, []byte{0xc0, 0}},
		{-1, []byte{0x7f}}, {-64, []byte{0x40}},
		{-65, []byte{0xbf, 0x7f}},
		{math.MinInt32, []byte{0x80, 0x80, 0x80, 0x80, 0x78}},
	} {
		o := &out{}
		o.sleb(c.v)
		if !bytes.Equal(o.b, c.want) {
			t.Errorf("sleb(%d) = %x, want %x", c.v, o.b, c.want)
		}
		r := &reader{b: o.b}
		if got := r.sleb(); got != c.v || r.err != nil {
			t.Errorf("read back %d, %v", got, r.err)
		}
	}
	o := &out{}
	o.uleb(300)
	if !bytes.Equal(o.b, []byte{0xac, 0x02}) {
		t.Errorf("uleb(300) = %x", o.b)
	}
	// Shared prefixes longer than 31 take more than one length byte.
	w := &writer{strs: map[string]bool{}}
	long := strings.Repeat("x", 40)
	w.str(long + "a")
	w.str(long + "b")
	w.str("y")
	o = &out{strs: map[string]int{}}
	w.writeStrings(o)
	r := &reader{b: o.b}
	f := &ghwFile{}
	r.readStrings(f)
	if r.err != nil || len(f.strs) != 4 || f.strs[2] != long+"b" ||
		f.strs[3] != "y" {
		t.Errorf("strings %q, %v", f.strs, r.err)
	}
	if !bytes.Contains(o.b, []byte{8 | 0x80, 1, 'b'}) {
		t.Errorf("prefix length 40 not written as 8+0x80, 1: %x", o.b)
	}
}
