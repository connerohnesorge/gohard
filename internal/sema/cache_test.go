package sema

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/cache"
	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
)

// graphBytes is the whole object graph of a project result in the
// codec's cache form (the bundle's objects by number, nodes by path):
// two results give the same bytes exactly when they are the same graph
// of the same values, sharing included.
func graphBytes(t testing.TB, r *Result) []byte {
	t.Helper()
	return graphOf(t, r, r.Files, r.Units, r.Libraries, true)
}

// graphOf is graphBytes of some of r's files, units and libraries.
func graphOf(t testing.TB, r *Result, files []*File, units []*Unit,
	libs []*Library, rest bool,
) []byte {
	t.Helper()
	s := &session{base: r.Bundle, files: r.Files}
	uc := &unitCache{s: s, unitIdx: map[*Unit]int{},
		fileIdx: map[*File]int{}, libIdx: map[*Library]int{},
		libDecl: map[*Decl]int{}, roots: map[*cst.Green]uint64{}}
	for i, f := range r.Files {
		uc.roots[f.Parse.Root] = uint64(2 * i)
	}
	for i, f := range r.Bundle.Files {
		if _, ok := uc.roots[f.Parse.Root]; !ok {
			uc.roots[f.Parse.Root] = uint64(2*i + 1)
		}
	}
	e := newEncoder()
	e.x = &xenc{uc: uc, owners: map[int]bool{}, paths: true}
	seedTables(func(ts []*Type, es []*eval.Type) {
		e.t.p_Type.seed(ts...)
		e.t.p_eval_Type.seed(es...)
	})
	ok := catchCodec(func() {
		for _, f := range files {
			e.p_File(f)
		}
		for _, u := range units {
			e.p_Unit(u)
		}
		for _, l := range libs {
			e.p_Library(l)
		}
		if rest {
			for i := range r.Diagnostics {
				e.s_diag_Diagnostic(&r.Diagnostics[i])
			}
			for _, s := range r.Incomplete {
				e.str(s)
			}
		}
		drain(&e.queue, &e.later)
	})
	if !ok || e.x.bad {
		t.Fatal("graphBytes: the result does not encode")
	}
	return e.buf
}

// cacheEnv is a simulated file system holding one cache folder.
type cacheEnv struct {
	sim *simenv.Sim
	dir string
	key cache.Key
}

func newCacheEnv(cfg config.Config) *cacheEnv {
	return &cacheEnv{sim: simenv.New(simenv.Options{}),
		dir: "/project/.gohard/cache",
		key: cache.NewKey("test", CacheSchema(), cfg)}
}

func (c *cacheEnv) open() *cache.Store {
	return cache.Open(c.sim.Env().FS, c.dir, c.key)
}

// entries returns the paths of the entry files, sorted.
func (c *cacheEnv) entries(t testing.TB) []string {
	t.Helper()
	var out []string
	for _, kind := range cache.Kinds {
		d := path.Join(c.dir, kind)
		es, err := c.sim.FS.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range es {
			out = append(out, path.Join(d, e.Name()))
		}
	}
	return out
}

// corrupt damages every entry file: truncated, a flipped bit, or
// emptied, by turns.
func (c *cacheEnv) corrupt(t testing.TB) int {
	t.Helper()
	n := 0
	for i, p := range c.entries(t) {
		b, err := c.sim.FS.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		switch i % 3 {
		case 0:
			b = b[:len(b)/2]
		case 1:
			b = bytes.Clone(b)
			b[len(b)/2] ^= 0x10
		case 2:
			b = nil
		}
		if err := c.sim.FS.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		n++
	}
	return n
}

func cachedInput(s snapshot, c sema0Cache, jobs env.Jobs, limit int,
) Input {
	return Input{Files: s.sources(s.texts, nil, nil), Config: s.cfg,
		Cache: c, Jobs: jobs, Limit: limit}
}

type sema0Cache = Cache

// TestCacheCorpus (#37 Done when): on every corpus file, a run with a
// cold cache, a warm cache and a corrupted cache gives exactly the
// result of a run without the cache, with sequential, shuffled and
// parallel jobs (run it with -race); the warm run restores every file
// and unit from the cache.
func TestCacheCorpus(t *testing.T) {
	for _, s := range corpusSnapshots(t) {
		want := graphBytes(t, Analyze(Input{
			Files: s.sources(s.texts, nil, nil), Config: s.cfg}))
		for _, jobs := range []struct {
			j     env.Jobs
			limit int
		}{{nil, 0}, {shuffled{uint64(len(s.name))}, 3},
			{realenv.New(realenv.Options{}).Jobs, 16}} {
			ce := newCacheEnv(s.cfg)
			check := func(stage string, st *cache.Store) *Result {
				t.Helper()
				r := Analyze(cachedInput(s, st, jobs.j, jobs.limit))
				if got := graphBytes(t, r); !bytes.Equal(got, want) {
					t.Fatalf("%s: %s cache: result differs from the "+
						"uncached run", s.name, stage)
				}
				return r
			}
			cold := ce.open()
			r := check("cold", cold)
			if st := cold.Stats(); st.Hits != 0 || st.Puts == 0 {
				t.Fatalf("%s: cold run: %+v", s.name, st)
			}
			warm := ce.open()
			check("warm", warm)
			units := len(r.Units)
			if st := warm.Stats(); st.Hits != len(r.Files)+units ||
				st.Puts != 0 {
				t.Fatalf("%s: warm run: %+v, want %d hits", s.name, st,
					len(r.Files)+units)
			}
			if ce.corrupt(t) == 0 {
				t.Fatalf("%s: no entries", s.name)
			}
			bad := ce.open()
			check("corrupted", bad)
			if st := bad.Stats(); st.Wipes == 0 {
				t.Fatalf("%s: corrupted run: %+v", s.name, st)
			}
			again := ce.open()
			check("rebuilt", again)
		}
	}
}

// cacheEdits are the edits of TestIncrementalCorpus plus the ones the
// cache keys must see through or catch: a comment rewritten with text
// of the same length (every position stays, so the units keep their
// keys), a number changed in place (same length and lines: only the
// node ID of the edited unit sees it) and the same with a unit added
// in a new file (the unit graph changes).
var cacheEdits = append(slices.Clone(edits[:len(edits)-1]),
	edit{"same-length-comment", func(b []byte) []byte {
		out := slices.Clone(b)
		i := bytes.Index(out, []byte("--"))
		for i >= 0 && i+2 < len(out) && out[i+2] != '\n' {
			if c := out[i+2]; c >= 'a' && c <= 'y' {
				out[i+2] = c + 1
				break
			}
			i++
		}
		return out
	}},
	edit{"digit", nextDigit},
	edit{"digit-new-unit", nextDigit},
	edits[len(edits)-1], // remove the file
)

// nextDigit raises the first digit 1-8 outside a comment that follows
// a space by one: a code edit that keeps the length and every line
// break.
func nextDigit(b []byte) []byte {
	out := slices.Clone(b)
	line := 0 // start of the current line
	for i, c := range out {
		if c == '\n' {
			line = i + 1
			continue
		}
		if c >= '1' && c <= '8' && i > 0 && out[i-1] == ' ' &&
			!bytes.Contains(out[line:i], []byte("--")) {
			out[i] = c + 1
			break
		}
	}
	return out
}

// TestCacheEdits: after every edit of every corpus file, a run with
// the cache written before the edit (and by the edits before it) gives
// exactly the result of a run without the cache, and so does undoing
// the edit. The edits restore some units and analyze others again, so
// the keys are what is under test: a stale entry would show here.
func TestCacheEdits(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	hits, runs := 0, 0
	for _, s := range corpusSnapshots(t) {
		ce := newCacheEnv(s.cfg)
		Analyze(cachedInput(s, ce.open(), nil, 0))
		for _, p := range s.paths {
			for _, e := range cacheEdits {
				texts := maps.Clone(s.texts)
				if e.text == nil {
					delete(texts, p)
				} else {
					texts[p] = e.text(s.texts[p])
				}
				texts["zz_added.vhd"] = nil
				if e.name == "digit-new-unit" {
					texts["zz_added.vhd"] = []byte(
						"entity zz_added is end;\n")
				}
				if texts["zz_added.vhd"] == nil {
					delete(texts, "zz_added.vhd")
				}
				if len(texts) == 0 {
					continue
				}
				srcs := s.sources(texts, nil, nil)
				want := graphBytes(t, Analyze(Input{Files: srcs,
					Config: s.cfg}))
				st := ce.open()
				got := graphBytes(t, Analyze(Input{Files: srcs,
					Config: s.cfg, Cache: st}))
				runs++
				hits += st.Stats().Hits
				if !bytes.Equal(got, want) {
					t.Fatalf("%s: %s %s: cached result differs (%s)",
						s.name, e.name, p, firstDiff(got, want))
				}
				undo := graphBytes(t, Analyze(cachedInput(s, ce.open(),
					nil, 0)))
				orig := graphBytes(t, Analyze(Input{
					Files: s.sources(s.texts, nil, nil), Config: s.cfg}))
				if !bytes.Equal(undo, orig) {
					t.Fatalf("%s: undo of %s %s: cached result differs",
						s.name, e.name, p)
				}
			}
		}
	}
	t.Logf("%d edited runs, %d entries restored", runs, hits)
	if hits == 0 {
		t.Fatal("no edited run restored an entry")
	}
}

// TestCacheSameLengthComment: a comment rewritten with text of the
// same length keeps every unit's key, so every unit is restored; the
// syntax entry of the file changes (its text does).
func TestCacheSameLengthComment(t *testing.T) {
	src := map[string]string{
		"p.vhd": "package p is\n  -- the width\n  constant w : " +
			"natural := 8;\nend;\n",
		"e.vhd": "use work.p.all;\nentity e is\n  -- a port\n" +
			"  port (x : in bit_vector(w - 1 downto 0));\nend;\n",
	}
	in := func(files map[string]string) Input {
		var srcs []Source
		for _, p := range slices.Sorted(maps.Keys(files)) {
			srcs = append(srcs, Source{Path: p, Text: []byte(files[p])})
		}
		return Input{Files: srcs, Config: config.Defaults()}
	}
	ce := newCacheEnv(config.Defaults())
	cold := in(src)
	cold.Cache = ce.open()
	Analyze(cold)
	edited := maps.Clone(src)
	edited["p.vhd"] = strings.Replace(src["p.vhd"], "the width",
		"the depth", 1)
	warm := in(edited)
	st := ce.open()
	warm.Cache = st
	got := graphBytes(t, Analyze(warm))
	if want := graphBytes(t, Analyze(in(edited))); !bytes.Equal(got,
		want) {
		t.Fatal("cached result differs")
	}
	// Two units restored, one file restored, one file parsed again.
	if s := st.Stats(); s.Hits != 3 || s.Puts != 1 {
		t.Fatalf("stats %+v, want 3 hits and 1 put", s)
	}
}

// TestCacheSameLengthCode: a code edit that keeps the length and every
// line break of the file (a literal or a name changed in place, in the
// edited unit or in a unit it depends on) keeps every position, so only
// the units' node IDs can tell the old text from the new: the cached
// run must still give exactly the uncached result, and the edited unit
// (and the units above it) are analyzed again.
func TestCacheSameLengthCode(t *testing.T) {
	src := map[string]string{
		"p.vhd": "package p is\n  constant c : bit := '1';\n" +
			"  constant w : natural := 4;\nend;\n",
		"e.vhd": "use work.p.all;\nentity e is\n" +
			"  port (x : in bit_vector(w - 1 downto 0));\nend;\n" +
			"architecture a of e is\n  signal s : bit := c;\n" +
			"begin\n  s <= x(0);\nend;\n",
	}
	in := func(files map[string]string) Input {
		var srcs []Source
		for _, p := range slices.Sorted(maps.Keys(files)) {
			srcs = append(srcs, Source{Path: p, Text: []byte(files[p])})
		}
		return Input{Files: srcs, Config: config.Defaults()}
	}
	for _, tc := range []struct {
		name, path, from, to, diag string
		puts                       int
	}{
		// the dependency's literal: p, e and a are analyzed again
		{"literal", "p.vhd", "'1'", "'X'", "found 'X'", 4},
		// a number in the dependency: the port width changes
		{"number", "p.vhd", ":= 4", ":= 7", "", 4},
		// a name in the edited unit: x(0) reads an undeclared y
		{"name", "e.vhd", "x(0)", "y(0)", "y", 2},
		// a name in a dependency: a no longer finds c
		{"dep-name", "p.vhd", "constant c", "constant d", "c", 4},
	} {
		ce := newCacheEnv(config.Defaults())
		cold := in(src)
		cold.Cache = ce.open()
		Analyze(cold)
		edited := maps.Clone(src)
		edited[tc.path] = strings.Replace(src[tc.path], tc.from, tc.to,
			1)
		if len(edited[tc.path]) != len(src[tc.path]) {
			t.Fatalf("%s: the edit changes the length", tc.name)
		}
		warm := in(edited)
		st := ce.open()
		warm.Cache = st
		r := Analyze(warm)
		want := Analyze(in(edited))
		if !bytes.Equal(graphBytes(t, r), graphBytes(t, want)) {
			t.Fatalf("%s: cached result differs from the uncached "+
				"run:\n%s\nwant:\n%s", tc.name, diagText(r),
				diagText(want))
		}
		if tc.diag != "" && !strings.Contains(diagText(r), tc.diag) {
			t.Fatalf("%s: no %q in\n%s", tc.name, tc.diag, diagText(r))
		}
		if s := st.Stats(); s.Puts != tc.puts {
			t.Fatalf("%s: stats %+v, want %d puts", tc.name, s, tc.puts)
		}
	}
}

// TestCacheCommentText: a unit whose stored result holds comment text
// (a message quoting a node with a comment inside) depends on the text
// of its file: after a comment edit of the same length it is analyzed
// again, and the message shows the new comment.
func TestCacheCommentText(t *testing.T) {
	src := "entity e is end;\narchitecture a of e is\n" +
		"  constant c : integer := 2 ** - -- note\n    3;\nbegin\nend;\n"
	in := func(text string) Input {
		return Input{Files: []Source{{Path: "a.vhd", Text: []byte(text)}},
			Config: config.Defaults()}
	}
	ce := newCacheEnv(config.Defaults())
	cold := in(src)
	cold.Cache = ce.open()
	Analyze(cold)
	edited := strings.Replace(src, "note", "nope", 1)
	warm := in(edited)
	st := ce.open()
	warm.Cache = st
	r := Analyze(warm)
	if !strings.Contains(diagText(r), "-- nope") {
		t.Fatalf("the message does not show the new comment:\n%s",
			diagText(r))
	}
	if !bytes.Equal(graphBytes(t, r), graphBytes(t, Analyze(in(edited)))) {
		t.Fatal("cached result differs")
	}
	// The entity is restored; the architecture (its entry is stale)
	// and the file are stored again.
	if s := st.Stats(); s.Puts != 2 {
		t.Fatalf("stats %+v, want 2 puts", s)
	}
}

// libraryTexts reads every VHDL file below testdata/<dir>, by slash
// path relative to it; nil when the submodule is not checked out.
func libraryTexts(t testing.TB, dir string) map[string][]byte {
	t.Helper()
	root := filepath.Join("../../testdata", filepath.FromSlash(dir))
	texts := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, e os.DirEntry,
		err error,
	) error {
		if err != nil || e.IsDir() || !(strings.HasSuffix(p, ".vhd") ||
			strings.HasSuffix(p, ".vhdl")) {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		texts[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil || len(texts) == 0 {
		return nil
	}
	return texts
}

// TestCacheLibraries: on real libraries (every file of each into one
// library, so many units also have errors), with parallel jobs, the
// cold and the warm cache give exactly the uncached result, and so do
// seeded edits of their files made with the warm cache: the
// dependency keys are tested on code that reads across hundreds of
// units.
func TestCacheLibraries(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	jobs := realenv.New(realenv.Options{}).Jobs
	for _, lib := range []struct {
		dir   string
		edits int
	}{{"libs/osvvm", 4}, {"libs/uvvm", 1}} {
		dir := lib.dir
		texts := libraryTexts(t, dir)
		if texts == nil {
			t.Logf("%s is not checked out", dir)
			continue
		}
		s := snapshot{name: dir, cfg: config.Defaults(), texts: texts}
		s.cfg.Standard = "2019"
		analyze := func(texts map[string][]byte, c Cache) []byte {
			return graphBytes(t, Analyze(Input{
				Files: s.sources(texts, nil, nil), Config: s.cfg,
				Jobs: jobs, Limit: 16, Cache: c}))
		}
		want := analyze(texts, nil)
		ce := newCacheEnv(s.cfg)
		if got := analyze(texts, ce.open()); !bytes.Equal(got, want) {
			t.Fatalf("%s: cold cache: result differs", dir)
		}
		st := ce.open()
		if got := analyze(texts, st); !bytes.Equal(got, want) {
			t.Fatalf("%s: warm cache: result differs", dir)
		}
		if st.Stats().Misses != 0 {
			t.Fatalf("%s: warm cache: %+v", dir, st.Stats())
		}
		paths := slices.Sorted(maps.Keys(texts))
		order := env.Shuffle(uint64(len(dir)), 7, len(paths))
		for k := range lib.edits {
			p := paths[order[k]]
			e := cacheEdits[k%len(cacheEdits)]
			edited := maps.Clone(texts)
			edited[p] = e.text(texts[p])
			if got := analyze(edited, ce.open()); !bytes.Equal(got,
				analyze(edited, nil)) {
				t.Fatalf("%s: %s %s: cached result differs", dir,
					e.name, p)
			}
		}
	}
}

func firstDiff(a, b []byte) string {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return fmt.Sprintf("byte %d of %d/%d", i, len(a), len(b))
		}
	}
	return fmt.Sprintf("lengths %d/%d", len(a), len(b))
}
