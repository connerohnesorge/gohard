package sema

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/parser"
	"go.yaml.in/yaml/v3"
)

// fingerprint renders everything observable about a result: the
// diagnostics, every reference (occurrence -> declaration file and
// span) and the incomplete list. Two results with equal fingerprints
// answer every query the same way.
func fingerprint(r *Result) string {
	var b strings.Builder
	b.WriteString(diagText(r))
	for _, f := range r.Files {
		for _, u := range f.Units {
			for _, ref := range u.Refs {
				fmt.Fprintf(&b, "%s:%d-%d %d -> ", f.Path, ref.Span.Start,
					ref.Span.End, ref.Kind)
				if d := ref.Decl; d != nil && d.File != nil {
					fmt.Fprintf(&b, "%s:%d %s", d.File.Path, d.Span.Start,
						d.Kind)
				} else if d != nil {
					fmt.Fprintf(&b, "%s %s", d.Name, d.Kind)
				}
				if o := ref.Overload; o != nil {
					fmt.Fprintf(&b, " [%d candidates]", len(o.Candidates))
				}
				b.WriteByte('\n')
			}
		}
	}
	fmt.Fprintln(&b, r.Incomplete)
	return b.String()
}

// designs are small multi-file projects, with errors, for the
// properties below.
var designs = []map[string]string{
	crossFile,
	{
		"a.vhd": `use work.p.all;
entity a is port (x : in t); end;
architecture rtl of a is
  signal s : t := zero;
begin
  s <= x;
  u: entity work.b port map (y => s);
end;
`,
		"b.vhd": `use work.p.all;
entity b is port (y : in t); end;
architecture rtl of b is begin end;
`,
		"p.vhd": `package p is
  type t is (zero, one);
  constant bad : natural := -3;
end;
`,
		"c.vhd": `entity c is end;
architecture rtl of c is
  signal q : integer := nope;
begin
end;
`,
		"d1.vhd": "entity twice is end;",
		"d2.vhd": "entity twice is end;",
	},
}

// permute returns the sources of files in the order given by seed.
func permute(files map[string]string, seed uint64) []Source {
	paths := slices.Sorted(mapsKeys(files))
	order := env.Shuffle(seed, 0, len(paths))
	var out []Source
	for _, i := range order {
		out = append(out, Source{Path: paths[i],
			Text: []byte(files[paths[i]])})
	}
	return out
}

// TestPermutation: results do not change when the input files are
// permuted (#34), for every design and many seeds.
func TestPermutation(t *testing.T) {
	for i, files := range designs {
		want := fingerprint(analyze(t, files, nil))
		for seed := uint64(1); seed <= 25; seed++ {
			r := Analyze(Input{Files: permute(files, seed),
				Config: config.Defaults()})
			if got := fingerprint(r); got != want {
				t.Fatalf("design %d, seed %d: results differ:\n%s\nwant\n%s",
					i, seed, got, want)
			}
		}
	}
}

// reversed runs jobs from the last index to the first.
type reversed struct{}

func (reversed) Run(_, n int, job func(int)) {
	for i := n - 1; i >= 0; i-- {
		job(i)
	}
}

// shuffled runs jobs in a seeded order.
type shuffled struct{ seed uint64 }

func (s shuffled) Run(_, n int, job func(int)) {
	for _, i := range env.Shuffle(s.seed, uint64(n), n) {
		job(i)
	}
}

// TestRunners: the job runner changes nothing (P3): sequential,
// reversed and shuffled orders give identical results.
func TestRunners(t *testing.T) {
	for i, files := range designs {
		want := fingerprint(analyze(t, files, nil))
		for _, j := range []env.Jobs{env.Sequential{}, reversed{},
			shuffled{7}, shuffled{99}} {
			r := Analyze(Input{Files: permute(files, 3),
				Config: config.Defaults(), Jobs: j, Limit: 4})
			if got := fingerprint(r); got != want {
				t.Fatalf("design %d, runner %T: results differ", i, j)
			}
		}
	}
}

// TestIncremental: analysis reusing trees parsed earlier (as the editor
// does for unchanged documents) equals a fresh batch analysis of the
// same snapshot.
func TestIncremental(t *testing.T) {
	for i, files := range designs {
		fresh := fingerprint(analyze(t, files, nil))
		popts := parser.FromConfig(config.Defaults(), "")
		var srcs []Source
		for _, s := range permute(files, 5) {
			s.Parse = parser.Parse(s.Text, popts)
			srcs = append(srcs, s)
		}
		r := Analyze(Input{Files: srcs, Config: config.Defaults()})
		if got := fingerprint(r); got != fresh {
			t.Fatalf("design %d: incremental differs from fresh", i)
		}
		// An edit then the undo of the edit gives the fresh result.
		edited := cloneFiles(files)
		for p := range edited {
			edited[p] += "\n-- edited\n"
			break
		}
		analyze(t, edited, nil)
		if got := fingerprint(analyze(t, files, nil)); got != fresh {
			t.Fatalf("design %d: analysis kept state across runs", i)
		}
	}
}

func cloneFiles(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// TestNoPanic: sema never panics on any tree the parser produces
// (#34): every per-rule parse test input, every saved FuzzParse input
// and truncations of the bundled sources, under every standard.
func TestNoPanic(t *testing.T) {
	var inputs [][]byte
	files, _ := filepath.Glob("../../testdata/parse/*/*.vhd*")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err == nil {
			inputs = append(inputs, b)
		}
	}
	fuzz, _ := filepath.Glob("../../testdata/fuzz/FuzzParse/*")
	for _, f := range fuzz {
		if b, ok := fuzzInput(f); ok {
			inputs = append(inputs, b)
		}
	}
	if len(inputs) < 100 {
		t.Fatalf("only %d inputs found", len(inputs))
	}
	// Truncated and spliced bundled text: deep, half-finished trees.
	nstd := Bundle().Files[7].Text // ieee/numeric_std.vhdl
	for _, n := range []int{100, 1000, 5000, 20000, len(nstd) / 2} {
		inputs = append(inputs, nstd[:min(n, len(nstd))])
	}
	for _, s := range []string{"1993", "2008", "2019"} {
		c := config.Defaults()
		c.Standard = s
		for i, in := range inputs {
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("std %s input %d panics: %v\n%s\n%s", s,
							i, p, in, debug.Stack())
					}
				}()
				Analyze(Input{Config: c, Files: []Source{
					{Path: "x.vhd", Text: in},
					{Path: "y.vhd", Text: in},
				}})
			}()
		}
	}
}

// fuzzInput decodes a `go test fuzz v1` file holding one []byte.
func fuzzInput(path string) ([]byte, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	lines := strings.SplitN(string(b), "\n", 2)
	if len(lines) < 2 || lines[0] != "go test fuzz v1" {
		return nil, false
	}
	v := strings.TrimSpace(lines[1])
	if !strings.HasPrefix(v, "[]byte(") || !strings.HasSuffix(v, ")") {
		return nil, false
	}
	s, err := strconv.Unquote(v[len("[]byte(") : len(v)-1])
	if err != nil {
		return nil, false
	}
	return []byte(s), true
}

// corpusFile is the part of testdata/corpus/corpus.yaml these tests
// read.
type corpusFile struct {
	Designs []corpusDesign `yaml:"designs"`
}

type corpusDesign struct {
	Name      string `yaml:"name"`
	Standard  string `yaml:"standard"`
	Milestone string `yaml:"milestone"`
	Files     []struct{ Path string }
	Config    string             `yaml:"config"`
	Defines   map[string]string  `yaml:"defines"`
	Expected  struct{ Exit int } `yaml:"expected"`
	Variants  []corpusDesign     `yaml:"variants"`
	GHDL      struct {
		Result string `yaml:"result"`
	} `yaml:"ghdl"`
}

// loadCorpus returns the corpus designs and a function analyzing one.
func loadCorpus(t *testing.T) ([]corpusDesign,
	func(dir string, d corpusDesign) *Result,
) {
	t.Helper()
	b, err := os.ReadFile("../../testdata/corpus/corpus.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cf corpusFile
	if err := yaml.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, d corpusDesign) *Result {
		c := config.Defaults()
		if d.Config != "" {
			cb, err := os.ReadFile(filepath.Join(dir, d.Config))
			if err != nil {
				t.Fatal(err)
			}
			c, _ = config.Parse(d.Config, cb)
		}
		c.Standard = d.Standard
		var srcs []Source
		for _, f := range d.Files {
			text, err := os.ReadFile(filepath.Join(dir, f.Path))
			if err != nil {
				t.Fatal(err)
			}
			srcs = append(srcs, Source{Path: f.Path, Text: text})
		}
		return Analyze(Input{Files: srcs, Config: c})
	}
	return cf.Designs, run
}

// TestCorpus analyzes every oracle corpus design (#8) under its
// standard and configuration. A v1.0 design and every variant GHDL
// analyzes must analyze without errors (their failures are elaboration
// and simulation results); the variants GHDL rejects at analysis must
// get an error. The v1.1 designs analyze without errors too: their
// constructs are delivered (prot_cond's last one, generic maps on
// subprogram calls, in Stage 2) or not constructs at all (gc_heap's
// garbage collection). Every result is independent of the file order.
func TestCorpus(t *testing.T) {
	designs, run := loadCorpus(t)
	if len(designs) != 15 {
		t.Fatalf("%d corpus designs, want 15", len(designs))
	}
	for _, d := range designs {
		dir := filepath.Join("../../testdata/corpus", d.Name)
		cases := append([]corpusDesign{d}, d.Variants...)
		for i, c := range cases {
			name := d.Name
			if i > 0 {
				name += "/" + c.Name
				if c.Config == "" {
					c.Config = d.Config
				}
				if c.Standard == "" {
					c.Standard = d.Standard
				}
			}
			r := run(dir, c)
			if len(r.Files) == 0 || len(r.Units) == 0 {
				t.Fatalf("%s: nothing analyzed", name)
			}
			errs := 0
			for _, dg := range r.Diagnostics {
				if dg.Severity == 1 {
					errs++
				}
			}
			analysisFails := c.GHDL.Result == "analysis-error" ||
				strings.Contains(c.GHDL.Result, "analy")
			switch {
			case analysisFails:
				if errs == 0 {
					t.Errorf("%s: GHDL rejects it, sema finds nothing",
						name)
				}
			default:
				if errs > 0 {
					t.Errorf("%s: unexpected errors:\n%s", name,
						diagText(r))
				}
			}
		}
	}
}

// TestCorpusRefs: in the corpus designs (all clean since the v1.1
// release) every identifier that is
// the name of a simple name resolves to a declaration (the LSP's
// definition and references need every occurrence, #25).
func TestCorpusRefs(t *testing.T) {
	designs, run := loadCorpus(t)
	checked := 0
	defer func() {
		if checked < 1000 {
			t.Errorf("only %d names checked", checked)
		}
	}()
	for _, d := range designs {
		r := run(filepath.Join("../../testdata/corpus", d.Name), d)
		for _, f := range r.Files {
			for n := range f.Root().Descendants() {
				if n.Kind() != kindSimpleName || n.Inactive() {
					continue
				}
				tok := n.FirstToken()
				checked++
				ref, ok := r.RefAt(f.Path, tok.Span().Start)
				if ok && (ref.Decl != nil || ref.Overload != nil) {
					continue
				}
				if _, ok := r.DeclAt(f.Path, tok.Span().Start); ok {
					continue
				}
				l, c := f.Lines.LineCol(tok.Span().Start)
				t.Errorf("%s:%d:%d: %s does not resolve", f.Path, l, c,
					tok.Text())
			}
		}
	}
}

const kindSimpleName = cst.KindSimpleName

// TestOSVVM analyzes OSVVM (testdata/libs/osvvm, the pinned submodule)
// as a VHDL-2019 project with the files its osvvm.pro script selects
// for 2019: real-world code with protected types, generic packages,
// aliases with signatures and heavy overloading must give no
// diagnostic (false positives are worse than misses in an editor).
func TestOSVVM(t *testing.T) {
	root := "../../testdata/libs/osvvm"
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) == 0 {
		t.Skip("testdata/libs/osvvm is not checked out")
	}
	skip := func(name string) bool {
		for _, s := range []string{"_Aldec", "_NVC", "_template",
			"MemoryPkgIs01"} {
			if strings.Contains(name, s) {
				return true
			}
		}
		return false
	}
	// osvvm.pro picks the deprecated/*_c.vhd stubs in place of these
	// for a tool without VHDL-2019 assert API, file/line/path and
	// std.env support, which v1.0 is (#18): the 2019 versions are
	// opaque (TestOSVVM2019Opaque in unsupported_test.go).
	stubbed := []string{"AssertApiPkg", "FileLinePathPkg",
		"LanguageSupport2019Pkg"}
	var srcs []Source
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".vhd") || skip(n) {
			continue
		}
		if slices.Contains(stubbed, strings.TrimSuffix(n, ".vhd")) {
			n = "deprecated/" + strings.TrimSuffix(n, ".vhd") + "_c.vhd"
		}
		b, err := os.ReadFile(filepath.Join(root, n))
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, Source{Path: "osvvm/" + n, Text: b})
	}
	c := config.Defaults()
	c.Standard = "2019"
	c.Libraries = []config.Library{{Name: "osvvm",
		Folders: []config.Folder{{Path: "osvvm"}}}}
	r := Analyze(Input{Files: srcs, Config: c})
	if len(srcs) < 40 || len(r.Units) < 80 {
		t.Fatalf("only %d files, %d units", len(srcs), len(r.Units))
	}
	if len(r.Diagnostics) > 0 {
		t.Fatalf("OSVVM:\n%s", diagText(r))
	}
}

// neorv32Sources returns the NEORV32 core and testbench sources from
// the pinned submodule, or nil when it is not checked out.
func neorv32Sources(t testing.TB, root string) []Source {
	t.Helper()
	var srcs []Source
	for _, g := range []string{"rtl/core/*.vhd", "sim/*.vhd"} {
		files, _ := filepath.Glob(filepath.Join(root, g))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			srcs = append(srcs, Source{Path: filepath.Base(f), Text: b})
		}
	}
	return srcs
}

// TestNEORV32 analyzes the NEORV32 core and testbench (the large
// project of #19) with no diagnostic.
func TestNEORV32(t *testing.T) {
	srcs := neorv32Sources(t, "../../testdata/neorv32")
	if len(srcs) == 0 {
		t.Skip("testdata/neorv32 is not checked out")
	}
	c := config.Defaults()
	c.Libraries = []config.Library{{Name: "neorv32",
		Folders: []config.Folder{{Path: "."}}}}
	r := Analyze(Input{Files: srcs, Config: c})
	if len(r.Diagnostics) > 0 {
		t.Fatalf("NEORV32:\n%s", diagText(r))
	}
}

// TestStop: a stopped analysis returns nil at a level boundary, and
// polling more than the number of levels never stops a complete run.
func TestStop(t *testing.T) {
	n := 0
	r := Analyze(Input{Files: permute(designs[1], 1),
		Config: config.Defaults(), Stop: func() bool {
			n++
			return n > 2
		}})
	if r != nil {
		t.Fatal("stopped analysis returned a result")
	}
	r = Analyze(Input{Files: permute(designs[1], 1),
		Config: config.Defaults(), Stop: func() bool { return false }})
	if r == nil || fingerprint(r) != fingerprint(analyze(t, designs[1],
		nil)) {
		t.Fatal("a never-stopped analysis differs")
	}
}
