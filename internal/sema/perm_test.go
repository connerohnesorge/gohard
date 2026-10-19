package sema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
	"go.yaml.in/yaml/v3"
)

// permSet is one set of analysis inputs for the permutation tests.
type permSet struct {
	name string
	srcs []Source
	cfg  config.Config
}

// maxExhaustive is the largest set every ordering of which is analyzed
// (6! = 720 orders); larger sets get randomOrders seeded orders.
// Sets over largeSet files (OSVVM, NEORV32) get the reverse and two
// random orders, and are skipped under -short.
const (
	maxExhaustive = 6
	randomOrders  = 12
	largeSet      = 40
)

// permutations calls f with every ordering of 0..n-1 (Heap's algorithm,
// deterministic).
func permutations(n int, f func([]int)) {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	c := make([]int, n)
	f(p)
	for i := 0; i < n; {
		if c[i] < i {
			if i%2 == 0 {
				p[0], p[i] = p[i], p[0]
			} else {
				p[c[i]], p[i] = p[i], p[c[i]]
			}
			f(p)
			c[i]++
			i = 0
		} else {
			c[i] = 0
			i++
		}
	}
}

// orders returns the orderings the permutation test analyzes for n
// files: all of them for small sets, seeded random ones for large sets
// (always including the reverse order).
func orders(n int) [][]int {
	var out [][]int
	if n <= maxExhaustive {
		permutations(n, func(p []int) {
			out = append(out, append([]int(nil), p...))
		})
		return out
	}
	rev := make([]int, n)
	for i := range rev {
		rev[i] = n - 1 - i
	}
	out = append(out, rev)
	for seed := uint64(1); seed <= randomOrders; seed++ {
		out = append(out, env.Shuffle(seed, 17, n))
	}
	return out
}

func reorder(srcs []Source, order []int) []Source {
	out := make([]Source, len(order))
	for i, j := range order {
		out[i] = srcs[j]
	}
	return out
}

// corpusSets returns every corpus design and variant (#8) with its
// configuration, as written in corpus.yaml.
func corpusSets(t *testing.T) []permSet {
	t.Helper()
	b, err := os.ReadFile("../../testdata/corpus/corpus.yaml")
	if err != nil {
		t.Fatal(err)
	}
	type file struct {
		Path string `yaml:"path"`
	}
	type design struct {
		Name     string `yaml:"name"`
		Standard string `yaml:"standard"`
		Config   string `yaml:"config"`
		Files    []file `yaml:"files"`
		Variants []struct {
			Name     string `yaml:"name"`
			Standard string `yaml:"standard"`
			Config   string `yaml:"config"`
			Files    []file `yaml:"files"`
		} `yaml:"variants"`
	}
	var cf struct {
		Designs []design `yaml:"designs"`
	}
	if err := yaml.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	load := func(dir, name, std, cfgPath string, files []file) permSet {
		c := config.Defaults()
		if cfgPath != "" {
			cb, err := os.ReadFile(filepath.Join(dir, cfgPath))
			if err != nil {
				t.Fatal(err)
			}
			c, _ = config.Parse(cfgPath, cb)
		}
		c.Standard = std
		ps := permSet{name: name, cfg: c}
		for _, f := range files {
			text, err := os.ReadFile(filepath.Join(dir, f.Path))
			if err != nil {
				t.Fatal(err)
			}
			ps.srcs = append(ps.srcs, Source{Path: f.Path, Text: text})
		}
		// The stand-in models are library inputs too.
		for _, m := range c.Simulation.Models {
			text, err := os.ReadFile(filepath.Join(dir, m.Path))
			if err == nil {
				ps.srcs = append(ps.srcs, Source{Path: m.Path, Text: text})
			}
		}
		return ps
	}
	var out []permSet
	for _, d := range cf.Designs {
		dir := filepath.Join("../../testdata/corpus", d.Name)
		out = append(out, load(dir, d.Name, d.Standard, d.Config, d.Files))
		for _, v := range d.Variants {
			std, cfg, files := v.Standard, v.Config, v.Files
			if std == "" {
				std = d.Standard
			}
			if cfg == "" {
				cfg = d.Config
			}
			if len(files) == 0 {
				files = d.Files
			}
			out = append(out, load(dir, d.Name+"/"+v.Name, std, cfg,
				files))
		}
	}
	return out
}

// librarySets are the larger library inputs: the CLI fixture projects
// of the library and opaque cases, OSVVM and NEORV32 (when checked
// out).
func librarySets(t *testing.T) []permSet {
	t.Helper()
	var out []permSet
	for _, f := range []struct{ dir, std string }{
		{"check-unsupported-v11", "2019"},
		{"check-encrypted-standin", "2008"},
		{"check-unmapped-library", "2008"},
		{"check-import-cycle", "2008"},
		{"check-duplicate-unit", "2008"},
	} {
		dir := filepath.Join("../../testdata/cli", f.dir, "in")
		c := config.Defaults()
		if cb, err := os.ReadFile(filepath.Join(dir,
			config.FileName)); err == nil {
			c, _ = config.Parse(config.FileName, cb)
		}
		c.Standard = f.std
		out = append(out, permSet{name: f.dir, srcs: readTree(t, dir),
			cfg: c})
	}
	if entries, err := os.ReadDir("../../testdata/libs/osvvm"); err ==
		nil && len(entries) > 0 {
		c := config.Defaults()
		c.Standard = "2019"
		c.Libraries = []config.Library{{Name: "osvvm",
			Folders: []config.Folder{{Path: "."}}}}
		out = append(out, permSet{name: "osvvm",
			srcs: readTree(t, "../../testdata/libs/osvvm"), cfg: c})
	}
	if srcs := neorv32Sources(t, "../../testdata/neorv32"); len(srcs) >
		0 {
		c := config.Defaults()
		c.Libraries = []config.Library{{Name: "neorv32",
			Folders: []config.Folder{{Path: "."}}}}
		out = append(out, permSet{name: "neorv32", srcs: srcs, cfg: c})
	}
	return out
}

// TestFilePermutations: identical results (diagnostics, every
// reference and the incomplete list) for every ordering of each corpus
// design's files and of the small library sets, and for seeded random
// orderings of the large ones (#34 Done when).
func TestFilePermutations(t *testing.T) {
	sets := append(corpusSets(t), librarySets(t)...)
	exhaustive := 0
	for _, ps := range sets {
		if testing.Short() && len(ps.srcs) > largeSet {
			continue
		}
		want := fingerprint(Analyze(Input{Files: ps.srcs, Config: ps.cfg}))
		ords := orders(len(ps.srcs))
		if len(ps.srcs) > largeSet {
			ords = ords[:3] // reverse and two random orders
		}
		if len(ps.srcs) <= maxExhaustive && len(ps.srcs) > 1 {
			exhaustive++
		}
		for _, o := range ords {
			r := Analyze(Input{Files: reorder(ps.srcs, o), Config: ps.cfg})
			if got := fingerprint(r); got != want {
				t.Fatalf("%s: order %v changes the result:\n%s\nwant\n%s",
					ps.name, o, diagText(r), want[:min(len(want), 2000)])
			}
		}
	}
	if exhaustive < 10 {
		t.Errorf("only %d sets permuted exhaustively", exhaustive)
	}
}

// TestJobLimits: the real goroutine pool at -p 1 and -p 16 gives the
// sequential result on every set (#20: fixtures agree at -p 1 and -p
// 16).
func TestJobLimits(t *testing.T) {
	jobs := realenv.New(realenv.Options{}).Jobs
	for _, ps := range append(corpusSets(t), librarySets(t)...) {
		want := fingerprint(Analyze(Input{Files: ps.srcs, Config: ps.cfg}))
		for _, limit := range []int{1, 16} {
			r := Analyze(Input{Files: ps.srcs, Config: ps.cfg, Jobs: jobs,
				Limit: limit})
			if fingerprint(r) != want {
				t.Fatalf("%s: -p %d changes the result", ps.name, limit)
			}
		}
	}
}

// TestPermutationsHelper: permutations yields n! distinct orders.
func TestPermutationsHelper(t *testing.T) {
	for n := 0; n <= 5; n++ {
		seen := map[string]bool{}
		permutations(n, func(p []int) {
			seen[string(rune(len(p)))+fmtInts(p)] = true
		})
		want := 1
		for i := 2; i <= n; i++ {
			want *= i
		}
		if len(seen) != want {
			t.Errorf("n=%d: %d orders, want %d", n, len(seen), want)
		}
	}
}

func fmtInts(p []int) string {
	b := make([]byte, 0, len(p))
	for _, v := range p {
		b = append(b, byte('0'+v))
	}
	return string(b)
}
