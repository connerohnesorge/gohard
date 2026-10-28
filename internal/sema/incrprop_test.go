package sema

import (
	"bytes"
	"encoding/json"
	"flag"
	"maps"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
)

var incrSteps = flag.Int("incr.steps", 10,
	"edits per project in TestIncrementalEdits")

// incrProject is one project of TestIncrementalEdits.
type incrProject struct {
	name  string
	cfg   config.Config
	texts map[string][]byte
	// rounds is the number of edit sequences, each from texts (0 is
	// 1).
	rounds int
}

func incrProjects(t *testing.T) []incrProject {
	var out []incrProject
	for _, s := range corpusSnapshots(t) {
		out = append(out, incrProject{name: s.name, cfg: s.cfg,
			texts: s.texts})
	}
	// The rules design is small and reaches every rule of dirtyUnits
	// (incrrules_test.go): it gets many short edit sequences, as a
	// long one ends up broken beyond reuse (an envelope, a unit
	// removed).
	rules := incrProject{name: "rules", cfg: config.Defaults(),
		texts: map[string][]byte{}, rounds: 20}
	for k, v := range incrRulesDesign { // incrrules_test.go
		rules.texts[k] = []byte(v)
	}
	out = append(out, rules)
	if testing.Short() {
		return out
	}
	srcs := neorv32Sources(t, "../../testdata/neorv32")
	if len(srcs) == 0 {
		t.Fatal("testdata/neorv32 is not checked out: " +
			"git submodule update --init")
	}
	c := config.Defaults()
	c.Libraries = []config.Library{{Name: "neorv32",
		Folders: []config.Folder{{Path: "."}}}}
	p := incrProject{name: "neorv32", cfg: c, texts: map[string][]byte{}}
	for _, s := range srcs {
		p.texts[s.Path] = s.Text
	}
	return append(out, p)
}

// incrSnippets are inserted by the edits: declarations (some needing
// a body elsewhere, some with a static error), statements (a return
// changes what a function body gives another unit's static
// evaluation), a stray name, a comment, an unbalanced parenthesis.
var incrSnippets = []string{
	"x ", "signal zz9 : bit;\n", "constant k9 : integer := 3;\n",
	"constant k8 : natural := -1;\n", "function f9 return integer;\n",
	"procedure p9;\n", "  assert false;\n", "-- edited\n", "(",
	"type t9 is range 0 to 3;\n", "zz9 <= '1';\n", "end;\n",
	"return -1;\n",
}

// envelope is an encrypted envelope: it makes its unit opaque, which
// a result that reuses an earlier one cannot be.
const envelope = "`protect begin_protected\n`protect data_block\n" +
	"ABCDEF==\n`protect end_protected\n"

// incrEdit changes one file of texts, chosen with r, using the units
// of prev (the result of texts) to aim at a body (an architecture or
// a package body) or a signature (a primary unit): it inserts a
// snippet at a line start, deletes or duplicates a line, or puts back
// the file's text before its last edit (undo). It returns the path and
// a name.
func incrEdit(r *env.SplitMix64, prev *Result, texts map[string][]byte,
	history map[string][][]byte,
) (string, string) {
	pick := func(n int) int { return int(r.Uint64() % uint64(max(n, 1))) }
	f := prev.Files[pick(len(prev.Files))]
	text := texts[f.Path]
	kind := []string{"body", "signature", "anywhere", "undo"}[pick(4)]
	if kind == "undo" {
		if h := history[f.Path]; len(h) > 0 {
			texts[f.Path] = h[len(h)-1]
			history[f.Path] = h[:len(h)-1]
			return f.Path, kind
		}
		kind = "anywhere"
	}
	lo, hi := 0, len(text)
	var us []*Unit
	for _, u := range f.Units {
		body := u.Kind == UnitArchitecture || u.Kind == UnitPackageBody
		if kind == "anywhere" || body == (kind == "body") {
			us = append(us, u)
		}
	}
	if kind != "anywhere" && len(us) > 0 {
		sp := us[pick(len(us))].Node.Span()
		lo, hi = sp.Start, sp.End
	}
	var starts []int // line starts in [lo, hi]
	for i := lo; i <= hi && i <= len(text); i++ {
		if i == 0 || text[i-1] == '\n' {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		starts = []int{lo}
	}
	at := starts[pick(len(starts))]
	end := bytes.IndexByte(text[at:], '\n') + 1
	if end <= 0 {
		end = len(text) - at
	}
	history[f.Path] = append(history[f.Path], text)
	var out []byte
	switch op := pick(4); op {
	case 0: // delete the line
		out = slices.Concat(text[:at], text[at+end:])
		kind += "/delete"
	case 1: // duplicate the line
		out = slices.Concat(text[:at+end], text[at:at+end],
			text[at+end:])
		kind += "/duplicate"
	default:
		sn := incrSnippets[pick(len(incrSnippets))]
		if pick(16) == 0 {
			sn = envelope
		}
		out = slices.Concat(text[:at], []byte(sn), text[at:])
		kind += "/insert " + sn
	}
	texts[f.Path] = out
	return f.Path, kind
}

// TestIncrementalEdits (#53 Done when, P4): random edit sequences on
// every corpus design, on NEORV32 and on incrRulesDesign, analyzed the editor's way (the
// trees of unchanged files reused, the previous result passed as
// Input.Prev), with and without the on-disk cache. After every edit
// the result equals a full analysis of the same snapshot: the
// diagnostics byte for byte, the incomplete list and the whole object
// graph (graphBytes); and the previous result, which the analysis
// shares objects with, is unchanged (P3). Reuse is not vacuous: most
// steps reuse units of the previous result.
func TestIncrementalEdits(t *testing.T) {
	steps, reusing, shared, total := 0, 0, 0, 0
	for _, p := range incrProjects(t) {
		pSteps, pReusing := steps, reusing
		for round := range max(p.rounds, 1) {
			for _, withCache := range []bool{false, true} {
				var c Cache
				if withCache {
					c = newCacheEnv(p.cfg).open()
				}
				popts := parser.FromConfig(p.cfg, "")
				texts := map[string][]byte{}
				trees := map[string]*parser.Result{}
				for k, v := range p.texts {
					texts[k] = v
					trees[k] = parser.Parse(v, popts)
				}
				treeText := maps.Clone(texts)
				// Jobs in a shuffled order without the cache, in parallel
				// with it (run with -race).
				var jobs env.Jobs = shuffled{uint64(len(p.name))}
				if withCache {
					jobs = realenv.New(realenv.Options{}).Jobs
				}
				input := func(prev *Result) Input {
					s := snapshot{}
					return Input{Files: s.sources(texts, trees, treeText),
						Config: p.cfg, Cache: c, Prev: prev, Jobs: jobs,
						Limit: 16}
				}
				prev := Analyze(input(nil))
				history := map[string][][]byte{}
				r := env.SplitMix64{State: 0x9e37}
				for _, c := range p.name {
					r.State = r.State*31 + uint64(c)
				}
				r.State += uint64(round)
				for range *incrSteps {
					path, what := incrEdit(&r, prev, texts, history)
					trees[path] = parser.Parse(texts[path], popts)
					treeText[path] = texts[path]
					before := graphBytes(t, prev)
					incr := Analyze(input(prev))
					full := Analyze(Input{Files: snapshot{}.sources(texts,
						nil, nil), Config: p.cfg})
					steps++
					where := p.name + " " + path + " " + what
					if withCache {
						where += " (cache)"
					}
					n := checkIncremental(t, where, prev, before, incr, full)
					if n > 0 {
						reusing++
					}
					shared += n
					total += len(incr.Units)
					prev = incr
				}
			}
		}
		pSteps, pReusing = steps-pSteps, reusing-pReusing
		t.Logf("%s (%d files): %d of %d edits reused", p.name,
			len(p.texts), pReusing, pSteps)
		// A project of many files reuses on most edits.
		if len(p.texts) >= 20 && pReusing < pSteps/2 {
			t.Errorf("%s: only %d of %d edits reused the previous "+
				"result", p.name, pReusing, pSteps)
		}
	}
	t.Logf("%d edits, %d reused units of the previous result "+
		"(%d of %d units shared)", steps, reusing, shared, total)
	if reusing < steps/4 {
		t.Fatalf("only %d of %d edits reused the previous result",
			reusing, steps)
	}
}

// checkIncremental fails t unless incr, the analysis of a snapshot
// that reused prev, equals full, the full analysis of the same
// snapshot (the diagnostics byte for byte, the incomplete list and the
// object graph), and prev still encodes to before (P3). It returns how
// many of incr's units are prev's.
func checkIncremental(t *testing.T, where string, prev *Result,
	before []byte, incr, full *Result,
) int {
	t.Helper()
	gd, _ := json.Marshal(incr.Diagnostics)
	wd, _ := json.Marshal(full.Diagnostics)
	if !bytes.Equal(gd, wd) {
		t.Fatalf("%s: diagnostics differ:\n%s", where,
			firstDiffLine(diagText(incr), diagText(full)))
	}
	if !slices.Equal(incr.Incomplete, full.Incomplete) {
		t.Fatalf("%s: incomplete %v, want %v", where, incr.Incomplete,
			full.Incomplete)
	}
	if g, w := graphBytes(t, incr), graphBytes(t, full); !bytes.Equal(g,
		w) {
		t.Fatalf("%s: object graphs differ at %s", where, firstDiff(g, w))
	}
	if !bytes.Equal(graphBytes(t, prev), before) {
		t.Fatalf("%s: the previous result changed", where)
	}
	n := 0
	for i, u := range incr.Units {
		if i < len(prev.Units) && prev.Units[i] == u {
			n++
		}
	}
	return n
}
