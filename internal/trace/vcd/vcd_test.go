package vcd_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/trace"
	"github.com/connerohnesorge/gohard/v2/internal/trace/fst"
	"github.com/connerohnesorge/gohard/v2/internal/trace/tracetest"
	"github.com/connerohnesorge/gohard/v2/internal/trace/vcd"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestFoldTable(t *testing.T) {
	want := map[byte]byte{
		'U': 'x', 'X': 'x', '0': '0', '1': '1', 'Z': 'z', 'W': 'x',
		'L': '0', 'H': '1', '-': 'x',
	}
	for i := range len(trace.LogicChars) {
		c := trace.LogicChars[i]
		if got := vcd.Fold(c); got != want[c] {
			t.Errorf("Fold(%c) = %c, want %c", c, got, want[c])
		}
	}
	if len(want) != len(trace.LogicChars) {
		t.Fatal("the fold table must cover all nine values")
	}
}

// TestGolden pins the export of the sample trace, byte for byte and by
// its SHA-256 (testdata/sample.vcd.sha256): any change in the VCD bytes
// must be deliberate (#36).
func TestGolden(t *testing.T) {
	tr := tracetest.Sample()
	got := vcd.Export(tr)
	sum := sha256.Sum256(got)
	hash := hex.EncodeToString(sum[:]) + "\n"
	if *update {
		os.WriteFile("testdata/sample.vcd", got, 0o644)
		os.WriteFile("testdata/sample.vcd.sha256", []byte(hash), 0o644)
		return
	}
	want, err := os.ReadFile("testdata/sample.vcd")
	if err != nil {
		t.Fatal(err)
	}
	if n, a, b, ok := trace.FirstDiff(want, got); ok {
		t.Fatalf("line %d: want %q, got %q", n, a, b)
	}
	wantHash, _ := os.ReadFile("testdata/sample.vcd.sha256")
	if string(wantHash) != hash {
		t.Fatalf("sha256 %s, want %s", hash, wantHash)
	}
}

// TestStable: exporting the same trace (and a decoded copy of it) gives
// the same bytes every time.
func TestStable(t *testing.T) {
	for seed := range uint64(50) {
		r := trace.NewRecorder(nil)
		if err := tracetest.Random(seed, 8, 10).Feed(r); err != nil {
			t.Fatal(err)
		}
		tr, err := r.Trace()
		if err != nil {
			t.Fatal(err)
		}
		a, _ := vcd.Exporter{}.Export(tr)
		back, err := trace.Decode(trace.Encode(tr))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := vcd.Exporter{}.Export(back)
		if !bytes.Equal(a, b) {
			t.Fatalf("seed %d: export not stable", seed)
		}
	}
}

func TestSettledOnly(t *testing.T) {
	out := string(vcd.Export(tracetest.Sample()))
	if strings.Contains(out, "$date") {
		t.Error("VCD has a $date")
	}
	// tb.dut.bus.valid glitches true at 0+2 and back at 0+3: only the
	// settled false appears.
	if !strings.Contains(out, "sfalse #\n") ||
		strings.Contains(out, "strue") {
		t.Errorf("glitch exported:\n%s", out)
	}
	// One timestamp per time with settled changes, plus the end.
	if n := strings.Count(out, "\n#"); n != 8 {
		t.Errorf("%d timestamps, want 8", n)
	}
	for _, want := range []string{
		"$var wire 8 % count_q [7:0] $end",
		"$var wire 4 \" addr [3:0] $end",
		"$var integer 32 2 x $end",
		"$var real 64 , ratio $end",
		"$scope begin gen(0) $end",
		"bxzxx \"\n", // W Z - X
		"b0101 \"\n", // 0 1 L H
		"b11111111111111111111111111111011 2\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestBits(t *testing.T) {
	cases := []struct {
		lo, hi int64
		want   int
	}{
		{0, 0, 1}, {0, 1, 1}, {0, 2, 2}, {0, 255, 8}, {0, 256, 9},
		{-1, 0, 1}, {-2, 1, 2}, {-128, 127, 8}, {-129, 0, 9},
		{-2147483648, 2147483647, 32}, {0, 2147483647, 31},
		{-1 << 63, 1<<63 - 1, 64},
	}
	for _, c := range cases {
		if got := vcd.Bits(c.lo, c.hi); got != c.want {
			t.Errorf("Bits(%d, %d) = %d, want %d", c.lo, c.hi, got,
				c.want)
		}
	}
}

func TestIDsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := range 20000 {
		id := vcd.ID(i)
		if seen[id] {
			t.Fatalf("ID(%d) = %q repeats", i, id)
		}
		for k := range len(id) {
			if id[k] < '!' || id[k] > '~' {
				t.Fatalf("ID(%d) = %q", i, id)
			}
		}
		seen[id] = true
	}
}

// TestSeedNotExported: the run seed reaches neither the native text
// nor the VCD and FST exports, so a race-free design exports the same
// bytes at every seed (#36).
func TestSeedNotExported(t *testing.T) {
	a, b := tracetest.Sample(), tracetest.Sample()
	a.Header.Seed, b.Header.Seed = 0, 64
	if !bytes.Equal(trace.Encode(a), trace.Encode(b)) {
		t.Error("the canonical text depends on the seed")
	}
	if !bytes.Equal(vcd.Export(a), vcd.Export(b)) {
		t.Error("the VCD export depends on the seed")
	}
	fa, _ := fst.Exporter{}.Export(a)
	fb, _ := fst.Exporter{}.Export(b)
	if !bytes.Equal(fa, fb) {
		t.Error("the FST export depends on the seed")
	}
}
