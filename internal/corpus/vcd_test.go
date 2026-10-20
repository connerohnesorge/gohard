package corpus

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/trace"
)

// ghdlVCD is a GHDL-style dump of tb: package scopes, a port sharing
// its actual's id in NVC style, a std_logic glitch-free history, a
// vector, a negative integer and a boolean as a 1-bit reg.
const ghdlVCD = `$date
  today
$end
$timescale
  1 fs
$end
$scope module standard $end
$upscope $end
$scope module tb $end
$var reg 1 ! a $end
$var reg 4 " v[3:0] $end
$var integer 32 # n $end
$var reg 1 $ b $end
$scope module dut $end
$var reg 1 ! a $end
$var reg 0 % s $end
$upscope $end
$upscope $end
$enddefinitions $end
#0
$dumpvars
u!
bUUUU "
b0 #
0$
$end
#1000
1!
b10 "
1!
b11111111111111111111111111111011 #
#2000
0!
1$
#3000
0!
`

// canonicalTrace is GoHard's native trace of the same run, with a
// zero-width glitch on tb.a at 2000 fs that the settled projection
// drops.
const canonicalTrace = `gohard-trace 1
testbench tb
end 3000
type boolean enum false true
type integer integer -2147483648 2147483647
type std_logic logic
type std_logic_vector array std_logic
scope tb instance tb(sim)
scope tb.dut instance dut(rtl)
signal tb.a std_logic signal
signal tb.b boolean signal
signal tb.dut.a std_logic in
signal tb.n integer signal
signal tb.v std_logic_vector signal 3 downto 0
process 1 tb.p
0 0 tb.a U -
0 0 tb.b false -
0 0 tb.dut.a U -
0 0 tb.n 0 -
0 0 tb.v UUUU -
1000 1 tb.a 1 p1
1000 1 tb.dut.a 1 p1
1000 1 tb.n -5 p1
1000 1 tb.v 0010 p1
2000 1 tb.a 0 p1
2000 1 tb.b true p1
2000 1 tb.dut.a 0 p1
2000 2 tb.a 1 p1
2000 2 tb.dut.a 1 p1
2000 3 tb.a 0 p1
2000 3 tb.dut.a 0 p1
`

func TestParseVCD(t *testing.T) {
	o, err := ParseVCD([]byte(ghdlVCD), "TB")
	if err != nil {
		t.Fatal(err)
	}
	var sigs []string
	for _, s := range o.Signals {
		sigs = append(sigs, s.Path+" "+string(s.Class)+" "+
			strconv.Itoa(s.Width))
	}
	want := []string{"tb.a logic 1", "tb.b logic 1", "tb.dut.a logic 1",
		"tb.n integer 32", "tb.v logic 4"}
	if !slices.Equal(sigs, want) {
		t.Fatalf("signals %v, want %v", sigs, want)
	}
	got := string(changeText(o.Changes))
	wantC := "0 tb.a U\n0 tb.b 0\n0 tb.dut.a U\n0 tb.n 0\n0 tb.v UUUU\n" +
		"1000 tb.a 1\n1000 tb.dut.a 1\n1000 tb.n -5\n1000 tb.v 0010\n" +
		"2000 tb.a 0\n2000 tb.b 1\n2000 tb.dut.a 0\n"
	if got != wantC {
		t.Fatalf("changes:\n%s\nwant:\n%s", got, wantC)
	}
	if o.Total != len(o.Changes) {
		t.Fatalf("total %d", o.Total)
	}
}

func TestCompareOracle(t *testing.T) {
	tr, err := trace.Decode([]byte(canonicalTrace))
	if err != nil {
		t.Fatal(err)
	}
	o, err := ParseVCD([]byte(ghdlVCD), "tb")
	if err != nil {
		t.Fatal(err)
	}
	if p := CompareOracle(tr, o); len(p) > 0 {
		t.Fatalf("problems: %v", p)
	}
	// Any changed value is found, at its line.
	for i := range o.Changes {
		m := *o
		m.Changes = slices.Clone(o.Changes)
		m.Changes[i].Value += "X"
		m.Total, m.Digest = digest(m.Changes)
		p := CompareOracle(tr, &m)
		if len(p) != 1 || !strings.Contains(p[0],
			"change "+strconv.Itoa(i+1)+":") {
			t.Errorf("change %d: %v", i, p)
		}
	}
	// A signal GoHard does not record is reported.
	m := *o
	m.Signals = append(slices.Clone(o.Signals), OracleSignal{
		Path: "tb.zz", Class: ClassLogic, Width: 1})
	if p := CompareOracle(tr, &m); len(p) != 1 ||
		!strings.Contains(p[0], "tb.zz") {
		t.Errorf("missing signal: %v", p)
	}
}

// TestCompareTruncated: past OracleLines only the digest is kept, and
// a difference there is still found.
func TestCompareTruncated(t *testing.T) {
	var b strings.Builder
	b.WriteString("$scope module tb $end\n$var integer 32 ! n $end\n" +
		"$upscope $end\n$enddefinitions $end\n")
	var tb strings.Builder
	tb.WriteString("gohard-trace 1\ntestbench tb\nend 0\n" +
		"type integer integer -2147483648 2147483647\n" +
		"signal tb.n integer signal\nprocess 1 tb.p\n")
	n := OracleLines + 10
	for i := range n {
		b.WriteString("#" + strconv.Itoa(i) + "\nb" +
			strconv.FormatInt(int64(i), 2) + " !\n")
		tb.WriteString(strconv.Itoa(i) + " 1 tb.n " + strconv.Itoa(i) +
			" p1\n")
	}
	end := strings.Replace(tb.String(), "end 0", "end "+
		strconv.Itoa(n-1), 1)
	tr, err := trace.Decode([]byte(end))
	if err != nil {
		t.Fatal(err)
	}
	o, err := ParseVCD([]byte(b.String()), "tb")
	if err != nil {
		t.Fatal(err)
	}
	o.Tool, o.Version, o.Sources = "ghdl", "6.0.0", "abc"
	enc := o.Encode()
	back, err := DecodeOracle(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Changes) != OracleLines || back.Total != n {
		t.Fatalf("kept %d of %d", len(back.Changes), back.Total)
	}
	if p := CompareOracle(tr, back); len(p) > 0 {
		t.Fatalf("problems: %v", p)
	}
	back.Digest = strings.Repeat("0", 64)
	if p := CompareOracle(tr, back); len(p) != 1 ||
		!strings.Contains(p[0], "after change") {
		t.Fatalf("digest change: %v", p)
	}
}

// TestOracleRoundTrip: Decode(Encode(o)) == o for generated oracles.
func TestOracleRoundTrip(t *testing.T) {
	rng := &splitmix{1}
	for range 200 {
		o := &Oracle{Tool: "nvc", Version: "1.23.0", Sources: "abc",
			Testbench: "tb"}
		for i := range rng.IntN(5) + 1 {
			o.Signals = append(o.Signals, OracleSignal{
				Path: "tb.s" + strconv.Itoa(i), Class: ClassLogic,
				Width: rng.IntN(8) + 1})
		}
		var at uint64
		for range rng.IntN(50) {
			at += uint64(rng.IntN(3))
			s := o.Signals[rng.IntN(len(o.Signals))]
			o.Changes = append(o.Changes, OracleChange{Time: at,
				Path: s.Path, Value: strconv.Itoa(rng.IntN(100))})
		}
		o.Changes = sortChanges(o.Changes)
		o.limit()
		b, err := DecodeOracle(o.Encode())
		if err != nil {
			t.Fatal(err)
		}
		if string(b.Encode()) != string(o.Encode()) {
			t.Fatalf("round trip:\n%s\n%s", o.Encode(), b.Encode())
		}
	}
}

func TestDiffSignalsAndWithout(t *testing.T) {
	a, _ := ParseVCD([]byte(ghdlVCD), "tb")
	b := *a
	b.Changes = slices.Clone(a.Changes)
	b.Changes[0].Value = "X" // tb.a at 0
	if d := DiffSignals(a, &b); !slices.Equal(d, []string{"tb.a"}) {
		t.Fatalf("diff %v", d)
	}
	fa, err := a.Without([]string{"tb.a"})
	if err != nil {
		t.Fatal(err)
	}
	fb, _ := b.Without([]string{"tb.a"})
	if d := DiffSignals(fa, fb); len(d) > 0 {
		t.Fatalf("after Without: %v", d)
	}
	if _, ok := fa.Signal("tb.a"); ok {
		t.Fatal("tb.a kept")
	}
}

// splitmix is a fixed in-test PRNG (SplitMix64), so the generated
// cases are the same on every run without math/rand.
type splitmix struct{ s uint64 }

func (r *splitmix) IntN(n int) int {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	return int((z ^ z>>31) % uint64(n))
}

// TestCompareStopped: a design-stopped run's final time step is not in
// GHDL/NVC dumps, so CompareOracleStopped leaves it out and
// CompareOracle keeps it.
func TestCompareStopped(t *testing.T) {
	tr, err := trace.Decode([]byte(canonicalTrace + "3000 1 tb.n 7 p1\n"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := ParseVCD([]byte(ghdlVCD), "tb")
	if err != nil {
		t.Fatal(err)
	}
	if p := CompareOracleStopped(tr, o); len(p) > 0 {
		t.Fatalf("stopped: %v", p)
	}
	if p := CompareOracle(tr, o); len(p) != 1 {
		t.Fatalf("budget-ended run must keep the final step: %v", p)
	}
}

// TestCompareNVCUndumped: NVC leaves out time-0 values of signals with
// no event at 0; only an NVC recording is compared without them.
func TestCompareNVCUndumped(t *testing.T) {
	tr, err := trace.Decode([]byte(canonicalTrace))
	if err != nil {
		t.Fatal(err)
	}
	o, err := ParseVCD([]byte(ghdlVCD), "tb")
	if err != nil {
		t.Fatal(err)
	}
	m := *o
	m.Changes = nil
	for _, c := range o.Changes {
		if c.Time != 0 || c.Path != "tb.b" {
			m.Changes = append(m.Changes, c)
		}
	}
	m.Total, m.Digest = digest(m.Changes)
	m.Tool = "nvc"
	if p := CompareOracle(tr, &m); len(p) > 0 {
		t.Fatalf("nvc: %v", p)
	}
	m.Tool = "ghdl"
	if p := CompareOracle(tr, &m); len(p) != 1 {
		t.Fatalf("ghdl must dump every initial value: %v", p)
	}
}
