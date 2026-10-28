package sema

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
)

// snapshot is one analysis input of the incremental tests: a corpus
// design run (its files, standard and configuration).
type snapshot struct {
	name  string
	cfg   config.Config
	paths []string
	texts map[string][]byte
}

// corpusSnapshots returns every run of every oracle corpus design (#8):
// the design's files with its stand-in models, and each variant, under
// their standards and the design's gohard.yaml.
func corpusSnapshots(t testing.TB) []snapshot {
	t.Helper()
	const dir = "../../testdata/corpus"
	b, err := os.ReadFile(filepath.Join(dir, "corpus.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	type files []struct{ Path string }
	var m struct {
		Designs []struct {
			Name     string
			Standard string
			Config   string
			Files    files
			Models   map[string]string
			Variants []struct {
				Name     string
				Standard string
				Files    files
			}
		}
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var out []snapshot
	for _, d := range m.Designs {
		cfg := config.Defaults()
		if d.Config != "" {
			cb, err := os.ReadFile(filepath.Join(dir, d.Name, d.Config))
			if err != nil {
				t.Fatal(err)
			}
			cfg, _ = config.Parse(d.Config, cb)
		}
		add := func(name, std string, fs files, extra []string) {
			s := snapshot{name: name, cfg: cfg, texts: map[string][]byte{}}
			s.cfg.Standard = std
			var paths []string
			for _, f := range fs {
				paths = append(paths, f.Path)
			}
			for _, p := range append(extra, paths...) {
				text, err := os.ReadFile(filepath.Join(dir, d.Name, p))
				if err != nil {
					t.Fatal(err)
				}
				if _, dup := s.texts[p]; !dup {
					s.paths = append(s.paths, p)
				}
				s.texts[p] = text
			}
			out = append(out, s)
		}
		var models []string
		for _, k := range slices.Sorted(mapsKeys(d.Models)) {
			models = append(models, d.Models[k])
		}
		add(d.Name, d.Standard, d.Files, models)
		for _, v := range d.Variants {
			add(d.Name+"/"+v.Name, v.Standard, v.Files, nil)
		}
	}
	if len(out) < 14 {
		t.Fatalf("%d corpus runs", len(out))
	}
	return out
}

// edit is one change to one file of a snapshot; nil text removes the
// file.
type edit struct {
	name string
	text func(src []byte) []byte
}

// edits are the changes simulated on every file: a comment, a deleted
// line and a duplicated line (both usually make errors), the first half
// (a broken tree), an empty file and the file's removal.
var edits = []edit{
	{"comment", func(b []byte) []byte {
		return append(slices.Clone(b), "\n-- edited\n"...)
	}},
	{"delete-line", func(b []byte) []byte {
		ls := bytes.SplitAfter(b, []byte("\n"))
		k := len(ls) / 2
		return bytes.Join(slices.Delete(slices.Clone(ls), k, k+1), nil)
	}},
	{"duplicate-line", func(b []byte) []byte {
		ls := bytes.SplitAfter(b, []byte("\n"))
		k := len(ls) / 3
		return bytes.Join(slices.Insert(slices.Clone(ls), k, ls[k]), nil)
	}},
	{"half", func(b []byte) []byte { return slices.Clone(b[:len(b)/2]) }},
	{"empty", func([]byte) []byte { return []byte{} }},
	{"remove", nil},
}

// sources returns the snapshot's sources in path order, with Parse set
// from trees when the tree for that path was parsed from the same text.
func (s snapshot) sources(texts map[string][]byte,
	trees map[string]*parser.Result, treeText map[string][]byte,
) []Source {
	var out []Source
	for _, p := range slices.Sorted(maps2(texts)) {
		src := Source{Path: p, Text: texts[p]}
		if tr, ok := trees[p]; ok && bytes.Equal(treeText[p], texts[p]) {
			src.Parse = tr
		}
		out = append(out, src)
	}
	return out
}

func maps2(m map[string][]byte) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// TestIncrementalCorpus: on every corpus file (#34 Done when), an edit
// re-analyzed incrementally (the editor's way: the trees of the
// unchanged files are reused, only the edited file is parsed again,
// after an analysis of the snapshot before the edit) gives exactly the
// result of a fresh batch analysis of the edited snapshot, with the
// files in another order; undoing the edit gives the original result
// again.
func TestIncrementalCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	runs, changed := 0, 0
	for _, s := range corpusSnapshots(t) {
		popts := parser.FromConfig(s.cfg, "")
		trees := map[string]*parser.Result{}
		treeText := map[string][]byte{}
		for _, p := range s.paths {
			trees[p] = parser.Parse(s.texts[p], popts)
			treeText[p] = s.texts[p]
		}
		before := Analyze(Input{Files: s.sources(s.texts, trees,
			treeText), Config: s.cfg})
		want := fingerprint(before)
		for _, p := range s.paths {
			for _, e := range edits {
				texts := map[string][]byte{}
				for k, v := range s.texts {
					texts[k] = v
				}
				if e.text == nil {
					delete(texts, p)
				} else {
					texts[p] = e.text(s.texts[p])
				}
				if len(texts) == 0 {
					continue
				}
				// Incremental: reuse every unchanged tree; the edited
				// file is parsed by Analyze.
				incr := Analyze(Input{Files: s.sources(texts, trees,
					treeText), Config: s.cfg})
				// Fresh: nothing reused, the files in reverse order,
				// the jobs in a shuffled order.
				var fresh []Source
				for _, src := range s.sources(texts, nil, nil) {
					fresh = append([]Source{src}, fresh...)
				}
				batch := Analyze(Input{Files: fresh, Config: s.cfg,
					Jobs: shuffled{uint64(len(p))}, Limit: 3})
				got, exp := fingerprint(incr), fingerprint(batch)
				runs++
				if got != want {
					changed++
				}
				if got != exp {
					t.Fatalf("%s: %s %s: incremental differs from "+
						"batch:\n%s", s.name, e.name, p,
						firstDiffLine(got, exp))
				}
				// Undo: the snapshot before the edit, trees reused.
				undo := Analyze(Input{Files: s.sources(s.texts, trees,
					treeText), Config: s.cfg})
				if g := fingerprint(undo); g != want {
					t.Fatalf("%s: undo of %s %s differs:\n%s", s.name,
						e.name, p, firstDiffLine(g, want))
				}
			}
		}
	}
	// Every edit but the comment changes the result (spans move or
	// errors appear), so the comparison above is not vacuous.
	t.Logf("%d edits, %d changed the result", runs, changed)
	if changed < runs*2/3 {
		t.Fatalf("only %d of %d edits changed the result", changed, runs)
	}
}

// TestIncrementalEditSeries: a seeded series of edits on one corpus
// design, each re-analyzed with the trees of the previous step, equals
// the fresh analysis at every step.
func TestIncrementalEditSeries(t *testing.T) {
	for _, s := range corpusSnapshots(t) {
		if len(s.paths) < 2 {
			continue
		}
		popts := parser.FromConfig(s.cfg, "")
		texts := map[string][]byte{}
		trees := map[string]*parser.Result{}
		treeText := map[string][]byte{}
		for _, p := range s.paths {
			texts[p] = s.texts[p]
			trees[p] = parser.Parse(texts[p], popts)
			treeText[p] = texts[p]
		}
		order := env.Shuffle(uint64(len(s.name)), 1, 12)
		for step, k := range order {
			p := s.paths[k%len(s.paths)]
			e := edits[(k+step)%(len(edits)-1)] // never remove
			texts[p] = e.text(texts[p])
			incr := Analyze(Input{Files: s.sources(texts, trees,
				treeText), Config: s.cfg})
			fresh := Analyze(Input{Files: s.sources(texts, nil, nil),
				Config: s.cfg})
			if g, w := fingerprint(incr), fingerprint(fresh); g != w {
				t.Fatalf("%s step %d (%s %s):\n%s", s.name, step, e.name,
					p, firstDiffLine(g, w))
			}
			// The editor keeps the tree of the new text.
			trees[p] = parser.Parse(texts[p], popts)
			treeText[p] = texts[p]
		}
		break // one design keeps the series fast; the corpus test covers all
	}
}

// firstDiffLine shows the first differing line of two fingerprints.
func firstDiffLine(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range max(len(al), len(bl)) {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return "got:  " + x + "\nwant: " + y
		}
	}
	return "(equal)"
}
