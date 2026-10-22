package hier

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
)

var update = flag.Bool("update", false,
	"rewrite testdata/corpus/*.txt from the current hierarchy dumps")

// corpusDesign is the part of testdata/corpus/corpus.yaml these tests
// read.
type corpusDesign struct {
	Name      string `yaml:"name"`
	Standard  string `yaml:"standard"`
	Milestone string `yaml:"milestone"`
	Files     []struct{ Path string }
	Config    string            `yaml:"config"`
	Testbench string            `yaml:"testbench"`
	Defines   map[string]string `yaml:"defines"`
	Variants  []corpusDesign    `yaml:"variants"`
	Hierarchy string            `yaml:"hierarchy_oracle"`
}

const corpusDir = "../../../testdata/corpus"

func loadCorpus(t *testing.T) []corpusDesign {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(corpusDir, "corpus.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cf struct {
		Designs []corpusDesign `yaml:"designs"`
	}
	if err := yaml.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	return cf.Designs
}

// analyzeDesign analyzes a corpus design (or variant) with its files in
// the given order.
func analyzeDesign(t *testing.T, dir string, d corpusDesign,
	order []int,
) *sema.Result {
	t.Helper()
	c := config.Defaults()
	if d.Config != "" {
		cb, err := os.ReadFile(filepath.Join(dir, d.Config))
		if err != nil {
			t.Fatal(err)
		}
		c, _ = config.Parse(d.Config, cb)
	}
	c.Standard = d.Standard
	var srcs []sema.Source
	for _, i := range order {
		f := d.Files[i]
		text, err := os.ReadFile(filepath.Join(dir, f.Path))
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, sema.Source{Path: f.Path, Text: text})
	}
	return sema.Analyze(sema.Input{Files: srcs, Config: c})
}

// corpusCases returns every design and variant with its golden name.
func corpusCases(t *testing.T) (names []string, dirs []string,
	cases []corpusDesign,
) {
	for _, d := range loadCorpus(t) {
		dir := filepath.Join(corpusDir, d.Name)
		names, dirs, cases = append(names, d.Name), append(dirs, dir),
			append(cases, d)
		for _, v := range d.Variants {
			if v.Config == "" {
				v.Config = d.Config
			}
			if v.Standard == "" {
				v.Standard = d.Standard
			}
			if v.Testbench == "" {
				v.Testbench = d.Testbench
			}
			if len(v.Files) == 0 {
				v.Files = d.Files
			}
			names = append(names, d.Name+"."+v.Name)
			dirs, cases = append(dirs, dir), append(cases, v)
		}
	}
	return names, dirs, cases
}

// hierText is the golden form of a hierarchy: the dump, then the
// per-instance diagnostics and incomplete entries.
func hierText(h *Result) string {
	var b strings.Builder
	b.WriteString(h.Dump())
	for _, d := range h.Diagnostics {
		fmt.Fprintf(&b, "diagnostic %s:%d:%d %s %s\n", d.Path, d.Line,
			d.Col, d.Code, d.Message)
	}
	for _, p := range h.Incomplete {
		fmt.Fprintf(&b, "incomplete %s\n", p)
	}
	return b.String()
}

// analysisErrors reports whether analysis found errors (a variant that
// GHDL rejects at analysis has no hierarchy to compare).
func analysisErrors(r *sema.Result) bool {
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

// TestCorpusDumps elaborates every oracle corpus design and variant
// (#8) from its testbench and compares the hierarchy dump with its
// golden (testdata/corpus/<design>[.<variant>].txt; -update rewrites
// them). The dump is the same for every order of the design's files,
// and designs whose hierarchy oracle is hand-checked match their
// golden/hierarchy.txt.
func TestCorpusDumps(t *testing.T) {
	names, dirs, cases := corpusCases(t)
	if len(cases) < 14 {
		t.Fatalf("%d corpus cases", len(cases))
	}
	for i, d := range cases {
		t.Run(names[i], func(t *testing.T) {
			n := len(d.Files)
			fwd := make([]int, n)
			for j := range fwd {
				fwd[j] = j
			}
			rev := slices.Clone(fwd)
			slices.Reverse(rev)
			o := Options{Tops: []string{d.Testbench}}
			h := Build(analyzeDesign(t, dirs[i], d, fwd), o)
			got := hierText(h)
			if again := hierText(Build(analyzeDesign(t, dirs[i], d,
				rev), o)); again != got {
				t.Fatalf("file order changes the hierarchy:\n%s\n%s",
					got, again)
			}
			if len(h.Tops) != 1 &&
				!analysisErrors(analyzeDesign(t, dirs[i], d, fwd)) {
				t.Fatalf("testbench %s not elaborated:\n%s",
					d.Testbench, got)
			}
			golden := filepath.Join("testdata", "corpus", names[i]+".txt")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden),
					0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got),
					0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if got != string(want) {
				t.Errorf("hierarchy:\n%s\nwant:\n%s", got, want)
			}
			if !strings.HasPrefix(d.Hierarchy, "none") {
				return
			}
			// The hand-written hierarchy is recorded with the corpus
			// goldens (#8); until it is, the dump golden above stands.
			hand, err := os.ReadFile(filepath.Join(dirs[i], "golden",
				"hierarchy.txt"))
			if err != nil {
				t.Logf("no hand-checked hierarchy yet: %v", err)
				return
			}
			// Lines starting with # are notes, not compared.
			var kept []string
			for l := range strings.Lines(string(hand)) {
				if !strings.HasPrefix(l, "#") {
					kept = append(kept, l)
				}
			}
			if h.Dump() != strings.Join(kept, "") {
				t.Errorf("golden/hierarchy.txt:\n%s\ndump:\n%s", hand,
					h.Dump())
			}
		})
	}
}
