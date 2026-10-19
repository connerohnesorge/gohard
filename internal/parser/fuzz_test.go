package parser

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/span"
)

// fuzzDir holds the committed FuzzParse inputs (#30). Plain go test
// replays them, and the nightly fuzz job adds the crashers it finds.
const fuzzDir = "../../testdata/fuzz/FuzzParse"

// FuzzParse checks the #30 FuzzParse invariants on any bytes: Parse
// never panics or hangs (the default budget is never exhausted, which
// would be a parser bug), the tree round-trips byte for byte, every
// problem points at a real position of the input (also after the
// line/column conversion), and the parse is deterministic. Each input
// is parsed under VHDL-2008 defaults and under one more option set
// chosen by the input's hash, so the corpus covers every standard and
// PSL setting. The saved inputs under the repository's
// testdata/fuzz/FuzzParse/ replay with plain go test.
func FuzzParse(f *testing.F) {
	saved, err := filepath.Glob(filepath.Join(fuzzDir, "*"))
	if err != nil || len(saved) == 0 {
		f.Fatalf("no saved inputs under %s: %v", fuzzDir, err)
	}
	for _, name := range saved {
		b, err := readCorpusFile(name)
		if err != nil {
			f.Fatalf("%s: %v", name, err)
		}
		f.Add(b)
	}
	for _, p := range pieces {
		f.Add([]byte(p))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		h := fnv.New32a()
		h.Write(src)
		for _, opts := range []Options{{}, allOpts[h.Sum32()%
			uint32(len(allOpts))]} {
			checkFuzzInput(t, src, opts)
		}
	})
}

// checkFuzzInput checks the invariants FuzzParse adds on top of
// checkInvariants.
func checkFuzzInput(t *testing.T, src []byte, opts Options) {
	t.Helper()
	r := checkInvariants(t, "fuzz", src, opts)
	if r.TooLarge {
		t.Fatalf("%d bytes reported too large", len(src))
	}
	for _, p := range r.Problems {
		if p.Code == diag.SyntaxError && len(p.Args) > 0 &&
			strings.Contains(p.Args[0], "step budget") {
			t.Fatalf("default budget exhausted at %d: parser bug", p.Start)
		}
	}
	idx := span.NewIndex(src)
	for _, d := range r.Diagnostics("f.vhd") {
		start, ok1 := idx.OffsetOf(d.Line, d.Col)
		end, ok2 := idx.OffsetOf(d.EndLine, d.EndCol)
		if d.Path != "f.vhd" || !ok1 || !ok2 || start > end ||
			end > len(src) {
			t.Fatalf("diagnostic outside the input: %+v", d)
		}
	}
}

// readCorpusFile decodes a one-argument []byte Go fuzz corpus file.
func readCorpusFile(name string) ([]byte, error) {
	raw, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	head, body, _ := strings.Cut(string(raw), "\n")
	body = strings.TrimSpace(body)
	lit, ok := strings.CutPrefix(body, "[]byte(")
	lit, ok2 := strings.CutSuffix(lit, ")")
	if head != "go test fuzz v1" || !ok || !ok2 {
		return nil, fmt.Errorf("not a one-argument []byte corpus file")
	}
	s, err := strconv.Unquote(lit)
	return []byte(s), err
}

// TestFuzzCorpusFiles: every saved FuzzParse input is a valid corpus
// file, so the nightly job and plain go test read the same inputs.
func TestFuzzCorpusFiles(t *testing.T) {
	saved, _ := filepath.Glob(filepath.Join(fuzzDir, "*"))
	for _, name := range saved {
		if _, err := readCorpusFile(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
