package format

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/token"
)

// fuzzDir holds the committed FuzzFormat inputs (#30, #32). Plain go
// test replays them, and the nightly fuzz job adds the crashers it
// finds.
const fuzzDir = "../../testdata/fuzz/FuzzFormat"

var writeSeeds = flag.Bool("format.seeds", false,
	"write the parse goldens and corpus files as FuzzFormat inputs "+
		"under testdata/fuzz/FuzzFormat (existing files are kept)")

// FuzzFormat checks the #32 invariants (checkInvariants) on any input
// that parses: formatting twice equals formatting once; the only
// changes are whitespace and keyword case, so literals and extended
// identifiers are byte-identical; the output parses to the same tree
// (sameTree: same node kinds and token text ignoring spacing, keywords
// compared case-insensitively, the same comments in the same order).
// Inputs that do not parse are skipped. Each input runs under the
// defaults (VHDL-2008) and under one more parse and format option set
// chosen by the input's hash, so the corpus covers every standard,
// PSL setting and format.* value. The saved inputs under the
// repository's testdata/fuzz/FuzzFormat/ replay with plain go test.
func FuzzFormat(f *testing.F) {
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
	f.Fuzz(func(t *testing.T, src []byte) {
		for _, c := range fuzzConfigs(src) {
			if err := checkInvariants(src, c.popts, c.opts); err != nil {
				t.Fatalf("%+v %+v: %v", c.popts, c.opts,
					trunc(err.Error()))
			}
		}
	})
}

type fuzzConfig struct {
	popts parser.Options
	opts  Options
}

// fuzzConfigs returns the option sets an input is checked under: the
// defaults, and one set derived from the input's FNV-1a hash.
func fuzzConfigs(src []byte) []fuzzConfig {
	h := fnv.New64a()
	h.Write(src)
	x := h.Sum64()
	pick := func(n int) int {
		v := int(x % uint64(n))
		x /= uint64(n)
		return v
	}
	std := token.Standards[pick(len(token.Standards))]
	o := Options{
		Indent:        MinIndent + pick(8),
		KeywordCase:   Case(pick(3)),
		AlignPorts:    pick(2) == 1,
		MaxLineLength: MinMaxLineLength + pick(81),
		Standard:      std,
	}
	return []fuzzConfig{
		{parser.Options{Standard: token.Std2008}, Default()},
		{parser.Options{Standard: std, PSLComments: pick(2) == 1}, o},
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

// encodeCorpusFile is the Go fuzz corpus encoding of one []byte
// argument and the file name go test -fuzz gives it.
func encodeCorpusFile(b []byte) (name string, data []byte) {
	data = []byte("go test fuzz v1\n[]byte(" + strconv.Quote(string(b)) +
		")\n")
	return fmt.Sprintf("%x", sha256.Sum256(data))[:16], data
}

// seedFiles are the files the saved inputs were seeded from: every
// valid parse golden and every corpus design file.
func seedFiles(t *testing.T) []string {
	t.Helper()
	parse, err := filepath.Glob("../../testdata/parse/*/valid*.vhd")
	if err != nil || len(parse) == 0 {
		t.Fatalf("no parse goldens: %v", err)
	}
	var corpus []string
	err = filepath.WalkDir("../../testdata/corpus",
		func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() &&
				(strings.HasSuffix(p, ".vhd") ||
					strings.HasSuffix(p, ".vhdl")) {
				corpus = append(corpus, p)
			}
			return err
		})
	if err != nil || len(corpus) == 0 {
		t.Fatalf("no corpus files: %v", err)
	}
	return append(parse, corpus...)
}

// TestFuzzSeeds: every saved input is a valid corpus file, and every
// seed file is saved, so the nightly job starts from the parse goldens
// and the corpus. go test -run TestFuzzSeeds -format.seeds writes the
// missing ones.
func TestFuzzSeeds(t *testing.T) {
	saved, _ := filepath.Glob(filepath.Join(fuzzDir, "*"))
	have := map[string]bool{}
	for _, name := range saved {
		b, err := readCorpusFile(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		have[string(b)] = true
	}
	if *writeSeeds {
		if err := os.MkdirAll(fuzzDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range seedFiles(t) {
		src := mustRead(t, p)
		if have[string(src)] {
			continue
		}
		if !*writeSeeds {
			t.Errorf("%s is not a saved FuzzFormat input; run go test "+
				"./internal/format -run TestFuzzSeeds -format.seeds", p)
			continue
		}
		name, data := encodeCorpusFile(src)
		err := os.WriteFile(filepath.Join(fuzzDir, name), data, 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCorpusFileEncoding(t *testing.T) {
	// Property: decoding an encoded input gives the input back, for
	// every byte value.
	var all []byte
	for i := range 256 {
		all = append(all, byte(i))
	}
	for _, b := range [][]byte{nil, []byte("entity e is end;\r\n"), all,
		[]byte("\"\\`\x00\xff\u00a0")} {
		name, data := encodeCorpusFile(b)
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := readCorpusFile(p)
		if err != nil || string(got) != string(b) {
			t.Errorf("%q: got %q, %v", b, got, err)
		}
	}
}

func TestFuzzConfigs(t *testing.T) {
	// Property: the derived options are always valid (normalizing them
	// changes nothing), are a pure function of the input, and over a
	// range of inputs reach every standard and keyword case.
	stds := map[token.Standard]bool{}
	cases := map[Case]bool{}
	for i := range 2000 {
		src := []byte(strconv.Itoa(i))
		cs := fuzzConfigs(src)
		if fmt.Sprint(cs) != fmt.Sprint(fuzzConfigs(src)) {
			t.Fatal("not deterministic")
		}
		o := cs[1].opts
		if o != o.normalized() || o.Standard != cs[1].popts.Standard {
			t.Fatalf("invalid options %+v", o)
		}
		stds[o.Standard] = true
		cases[o.KeywordCase] = true
	}
	if len(stds) != len(token.Standards) || len(cases) != 3 {
		t.Errorf("reached standards %v, cases %v", stds, cases)
	}
}
