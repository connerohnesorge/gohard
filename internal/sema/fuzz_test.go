package sema

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
)

// analyzeOptions are the configurations FuzzAnalyze runs an input
// under: every standard, and PSL comments under 2008.
var analyzeOptions = func() []config.Config {
	var out []config.Config
	for _, s := range []string{"1993", "2002", "2008", "2019"} {
		c := config.Defaults()
		c.Standard = s
		out = append(out, c)
	}
	c := config.Defaults()
	c.PSL.Comments = true
	return append(out, c)
}()

// fuzzSeeds returns every saved FuzzParse input (#30: the repository's
// testdata/fuzz/FuzzParse) and every per-rule parse test input and
// golden source (testdata/parse/*/*.vhd*).
func fuzzSeeds(t testing.TB) [][]byte {
	t.Helper()
	var out [][]byte
	saved, _ := filepath.Glob("../../testdata/fuzz/FuzzParse/*")
	for _, f := range saved {
		b, ok := fuzzInput(f)
		if !ok {
			t.Fatalf("%s: not a []byte fuzz corpus file", f)
		}
		out = append(out, b)
	}
	if len(saved) == 0 {
		t.Fatal("no saved FuzzParse inputs")
	}
	parse, _ := filepath.Glob("../../testdata/parse/*/*.vhd*")
	for _, f := range parse {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	if len(parse) < 200 {
		t.Fatalf("only %d parse inputs", len(parse))
	}
	return out
}

// checkAnalyzeInput is the FuzzAnalyze invariant on one input under
// one configuration: Analyze never panics on any tree the parser
// produces, alone or next to a copy of itself in another file, and the
// two-file result does not depend on the file order.
func checkAnalyzeInput(t *testing.T, src []byte, c config.Config) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("std %s psl %t: Analyze panics: %v\ninput %q\n%s",
				c.Standard, c.PSL.Comments, p, src, debug.Stack())
		}
	}()
	Analyze(Input{Config: c, Files: []Source{{Path: "x.vhd",
		Text: src}}})
	a := Analyze(Input{Config: c, Files: []Source{
		{Path: "x.vhd", Text: src}, {Path: "y.vhd", Text: src}}})
	b := Analyze(Input{Config: c, Files: []Source{
		{Path: "y.vhd", Text: src}, {Path: "x.vhd", Text: src}}})
	if fingerprint(a) != fingerprint(b) {
		t.Fatalf("std %s: result depends on the file order\ninput %q",
			c.Standard, src)
	}
}

// FuzzAnalyze: sema never panics on any FuzzParse corpus input (#34
// Done when). Plain go test replays every saved FuzzParse input and
// every parse test input under every standard and with PSL comments;
// fuzzing runs each new input under 2008 and one more configuration
// chosen by its hash.
func FuzzAnalyze(f *testing.F) {
	for _, b := range fuzzSeeds(f) {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		h := fnv.New32a()
		h.Write(src)
		checkAnalyzeInput(t, src, analyzeOptions[2])
		checkAnalyzeInput(t, src,
			analyzeOptions[h.Sum32()%uint32(len(analyzeOptions))])
	})
}

// TestFuzzSeedsEveryOption: the seed inputs under every configuration
// (the fuzz replay above uses two of them per input).
func TestFuzzSeedsEveryOption(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	for _, src := range fuzzSeeds(t) {
		for _, c := range analyzeOptions {
			checkAnalyzeInput(t, src, c)
		}
	}
}

// TestNoPanicPrefixes: every prefix of every corpus file cut at a line
// end (half-finished units, open regions, missing ends) analyzes
// without a panic, with the rest of its design, under the design's
// standard.
func TestNoPanicPrefixes(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	for _, s := range corpusSnapshots(t) {
		for _, p := range s.paths {
			text := s.texts[p]
			for end := 0; end <= len(text); end++ {
				if end < len(text) && text[end] != '\n' {
					continue
				}
				texts := map[string][]byte{}
				for k, v := range s.texts {
					texts[k] = v
				}
				texts[p] = text[:end]
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("%s: %s cut at %d panics: %v\n%s",
								s.name, p, end, r, debug.Stack())
						}
					}()
					Analyze(Input{Files: s.sources(texts, nil, nil),
						Config: s.cfg})
				}()
			}
		}
	}
}
