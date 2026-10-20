package ide

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/resolve"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/server"
)

const featPkg = `library ieee;
use ieee.std_logic_1164.all;
package defs is
  type pair_t is record
    first  : integer;
    second : std_logic;
  end record;
  constant ZERO : integer := 0;
  function inc(x : integer) return integer;
  function inc(x : std_logic) return std_logic;
end package;
`

const featTop = `library ieee;
use ieee.std_logic_1164.all;
use work.defs.all;
entity top is
end entity;
architecture rtl of top is
  signal p : pair_t;
  signal n : integer;
begin
  -- one
  -- two
  process
  begin
    n <= p.first;
    n <= inc(true);
    wait;
  end process;
end architecture;
`

func featureProject() project {
	return project{"defs.vhd": []byte(featPkg), "top.vhd": []byte(featTop)}
}

func labels(items []server.CompletionItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Label)
	}
	return out
}

func offsetOf(t *testing.T, text, marker string, delta int) int {
	t.Helper()
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("no %q", marker)
	}
	return i + delta
}

func TestCompletionSelectedNames(t *testing.T) {
	an := analyze(t, snapshotOf(t, featureProject(), nil))
	q := server.Query{Snap: an.Snap, URI: uri("top.vhd"),
		Offset: offsetOf(t, featTop, "p.first", 2)}
	got := labels(an.Complete(q))
	if !slices.Equal(got, []string{"first", "second"}) {
		t.Fatalf("record fields: %v", got)
	}
	// After "p.fi" only first remains.
	q.Offset += 2
	if got := labels(an.Complete(q)); !slices.Equal(got,
		[]string{"first"}) {
		t.Fatalf("filtered fields: %v", got)
	}
	q.Offset = offsetOf(t, featTop, "work.defs", 5)
	got = labels(an.Complete(q))
	if !slices.Contains(got, "defs") || !slices.Contains(got, "top") {
		t.Fatalf("library units: %v", got)
	}
	q.Offset = offsetOf(t, featTop, "defs.all", 5)
	got = labels(an.Complete(q))
	for _, w := range []string{"pair_t", "ZERO", "inc", "all"} {
		if !slices.Contains(got, w) {
			t.Fatalf("package members %v miss %s", got, w)
		}
	}
	q.Offset = offsetOf(t, featTop, "ieee.std", 5)
	if got := labels(an.Complete(q)); !slices.Contains(got,
		"NUMERIC_STD") {
		t.Fatalf("ieee units: %v", got)
	}
}

func TestCompletionNamesLibrariesKeywords(t *testing.T) {
	an := analyze(t, snapshotOf(t, featureProject(), nil))
	q := server.Query{Snap: an.Snap, URI: uri("top.vhd"),
		Offset: offsetOf(t, featTop, "n <= p", 0)}
	items := an.Complete(q)
	got := labels(items)
	for _, w := range []string{"n", "p", "ZERO", "pair_t", "ieee",
		"work", "std", "process", "end", "context"} {
		if !slices.Contains(got, w) {
			t.Fatalf("completion misses %s", w)
		}
	}
	// Every item replaces the (empty) word before the cursor.
	for _, it := range items {
		if it.Edit == nil || it.Edit.Span != (server.Span{Start: q.Offset,
			End: q.Offset}) || it.Edit.NewText != it.Label {
			t.Fatalf("item %+v", it)
		}
	}
	// VHDL-93 has no context keyword (#34: the selected standard).
	p := featureProject()
	p["gohard.yaml"] = []byte("standard: \"1993\"\n")
	an93 := analyze(t, snapshotOf(t, p, nil))
	q.Snap = an93.Snap
	if slices.Contains(labels(an93.Complete(q)), "context") {
		t.Fatal("context is not a VHDL-93 keyword")
	}
}

// TestCompletionOverloadCandidates checks that completion inside an
// undecided call lists the same candidates and reasons as hover (#34).
func TestCompletionOverloadCandidates(t *testing.T) {
	an := analyze(t, snapshotOf(t, featureProject(), nil))
	off := offsetOf(t, featTop, "inc(true)", 4)
	q := server.Query{Snap: an.Snap, URI: uri("top.vhd"), Offset: off}
	h, ok := resolve.HoverAt(an.Result, "top.vhd",
		offsetOf(t, featTop, "inc(true)", 0))
	if !ok || h.Overload == nil || h.Overload.Chosen != nil {
		t.Fatalf("hover: %+v", h)
	}
	n := checkCandidates(t, an, q, h.Overload)
	if n != 2 {
		t.Fatalf("%d candidates", n)
	}
}

// checkCandidates checks that the completion at q starts with the
// candidates of o, each detail a line of the hover markdown.
func checkCandidates(t *testing.T, an *Analysis, q server.Query,
	o *sema.Overload,
) int {
	t.Helper()
	items := an.Complete(q)
	path, _ := an.Path(q.URI)
	n := 0
	for _, c := range o.Candidates {
		want := CandidateDetail(c)
		k := slices.IndexFunc(items, func(it server.CompletionItem) bool {
			return it.Detail == want
		})
		if k < 0 {
			continue // filtered out by the word before the cursor
		}
		h, ok := resolve.HoverAt(an.Result, path, q.Offset)
		if ok && h.Overload == o && !strings.Contains(h.Markdown,
			"- "+want) {
			t.Fatalf("hover %q lacks %q", h.Markdown, want)
		}
		n++
	}
	return n
}

// TestCompletionProperties checks on the corpus that completion is
// deterministic, filtered by the word before the cursor, free of
// repeats, and that at every name occurrence of a directly visible
// project declaration the declaration is offered.
func TestCompletionProperties(t *testing.T) {
	checked := 0
	for name, p := range corpus(t) {
		an := analyze(t, snapshotOf(t, p, nil))
		if an.Result == nil {
			continue
		}
		for _, f := range an.Result.Files {
			furi, ok := an.URI(f.Path)
			if !ok {
				continue
			}
			for _, u := range f.Units {
				for i, ref := range u.Refs {
					d := ref.Decl
					if d == nil || d.Implicit || d.File == nil ||
						d.File.Bundled || i%3 != 0 {
						continue
					}
					switch ref.Kind {
					case sema.RefRead, sema.RefWrite, sema.RefType:
					default:
						continue
					}
					s := ref.Span.Start
					if s > 0 && f.Text[s-1] == '.' {
						continue
					}
					word := string(f.Text[s:ref.Span.End])
					if !strings.EqualFold(word, d.Text) {
						continue
					}
					q := server.Query{Snap: an.Snap, URI: furi,
						Offset: ref.Span.End}
					items := an.Complete(q)
					seen := map[string]bool{}
					found := false
					for _, it := range items {
						k := strings.ToLower(it.Label) + "\x00" + it.Detail
						if seen[k] {
							t.Fatalf("%s %s: repeated %s", name, f.Path, k)
						}
						seen[k] = true
						if !strings.HasPrefix(strings.ToLower(it.Label),
							strings.ToLower(word)) {
							t.Fatalf("%s: %s not filtered by %s", name,
								it.Label, word)
						}
						found = found || strings.EqualFold(it.Label, d.Text)
					}
					if !found && visible(an, f.Path, s, d) {
						t.Fatalf("%s %s:%d: %s not offered (%v)", name,
							f.Path, s, d.Text, labels(items))
					}
					if !slices.EqualFunc(items, an.Complete(q),
						func(a, b server.CompletionItem) bool {
							return a.Label == b.Label && a.Detail == b.Detail
						}) {
						t.Fatal("completion is not deterministic")
					}
					checked++
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("nothing checked")
	}
	t.Logf("%d completions checked", checked)
}

func visible(an *Analysis, path string, off int, d *sema.Decl) bool {
	return slices.Contains(an.Result.VisibleAt(path, off), d)
}

func TestSymbols(t *testing.T) {
	an := analyze(t, snapshotOf(t, featureProject(), nil))
	ws := an.WorkspaceSymbols("")
	var names []string
	for _, s := range ws {
		names = append(names, s.Name)
	}
	for _, w := range []string{"defs", "top", "rtl", "pair_t", "ZERO"} {
		if !slices.Contains(names, w) {
			t.Fatalf("workspace symbols %v miss %s", names, w)
		}
	}
	if got := an.WorkspaceSymbols("PAIR"); len(got) != 1 ||
		got[0].Name != "pair_t" || got[0].URI != uri("defs.vhd") {
		t.Fatalf("query PAIR: %+v", got)
	}
	ds := an.DocumentSymbols(uri("defs.vhd"))
	if len(ds) != 1 || ds[0].Name != "defs" || len(ds[0].Children) == 0 {
		t.Fatalf("document symbols %+v", ds)
	}
}

// TestFoldingProperties checks the folding invariants on every corpus
// file and on the fixture: ranges span two lines or more, are sorted,
// distinct, inside the document, and nest (two ranges either nest or
// are disjoint by line).
func TestFoldingProperties(t *testing.T) {
	ps := corpus(t)
	ps["fixture"] = featureProject()
	n := 0
	for name, p := range ps {
		n += checkFolds(t, name, analyze(t, snapshotOf(t, p, nil)))
	}
	an := analyze(t, snapshotOf(t, featureProject(), nil))
	fs := an.Folding(uri("top.vhd"))
	d, _ := an.Snap.Document(uri("top.vhd"))
	var got []string
	for _, f := range fs {
		got = append(got, f.Kind+":"+
			strconv.Itoa(d.Index().Line(f.Span.Start))+"-"+
			strconv.Itoa(d.Index().Line(f.Span.End)))
	}
	want := []string{"imports:0-2", ":5-16", "comment:9-10", ":11-15"}
	if !slices.Equal(got, want) {
		t.Fatalf("folds %v, want %v", got, want)
	}
	if n == 0 {
		t.Fatal("no folds")
	}
}

// checkFolds checks the folding invariants on every file of an and
// returns the number of folds.
func checkFolds(t *testing.T, name string, an *Analysis) int {
	t.Helper()
	n := 0
	for _, d := range Files(an.Snap) {
		fs := an.Folding(d.URI)
		x := d.Index()
		type lr struct{ a, b int }
		var prev *lr
		var all []lr
		for _, f := range fs {
			r := lr{x.Line(f.Span.Start), x.Line(f.Span.End)}
			if r.b <= r.a || f.Span.End > len(d.Text()) {
				t.Fatalf("%s %s: bad fold %+v", name, d.Rel, r)
			}
			if prev != nil && (r.a < prev.a ||
				r.a == prev.a && r.b <= prev.b) {
				t.Fatalf("%s %s: unsorted %v %v", name, d.Rel, *prev, r)
			}
			for _, o := range all {
				if o.a < r.a && r.a <= o.b && o.b < r.b {
					t.Fatalf("%s %s: crossing %v %v", name, d.Rel, o, r)
				}
			}
			all = append(all, r)
			prev = &all[len(all)-1]
			n++
		}
	}
	return n
}

// TestFeaturesOnLibraries checks folding, the workspace symbols of the
// whole project and completion at every 50th occurrence on OSVVM, UVVM
// and NEORV32.
func TestFeaturesOnLibraries(t *testing.T) {
	for _, c := range []struct {
		name string
		p    func(testing.TB) project
	}{{"osvvm", osvvm}, {"uvvm", uvvm}, {"neorv32", neorv}} {
		t.Run(c.name, func(t *testing.T) {
			an := analyze(t, snapshotOf(t, c.p(t), nil))
			folds := checkFolds(t, c.name, an)
			ws := an.WorkspaceSymbols("")
			units := 0
			for _, u := range an.Result.Units {
				if u.Decl == nil || u.File.Bundled {
					continue
				}
				units++
				l, _ := resolve.LocationOf(u.Decl)
				want, _ := an.Location(l)
				if !slices.ContainsFunc(ws, func(s server.Symbol) bool {
					return s.Selection == want.Span && s.URI == want.URI
				}) {
					t.Fatalf("workspace symbols miss %s", u.Decl.Text)
				}
			}
			n := 0
			for _, f := range an.Result.Files {
				furi, ok := an.URI(f.Path)
				if !ok {
					continue
				}
				for _, u := range f.Units {
					for i, ref := range u.Refs {
						if i%50 != 0 {
							continue
						}
						q := server.Query{Snap: an.Snap, URI: furi,
							Offset: ref.Span.End}
						for _, it := range an.Complete(q) {
							if it.Label == "" {
								t.Fatal("empty label")
							}
						}
						n++
					}
				}
			}
			t.Logf("%d folds, %d units in workspace symbols, "+
				"%d completions", folds, units, n)
		})
	}
}

// TestHighlightsAreInFileReferences checks at every occurrence of the
// corpus that the highlights are the in-file references.
func TestHighlightsAreInFileReferences(t *testing.T) {
	n := 0
	for _, p := range corpus(t) {
		an := analyze(t, snapshotOf(t, p, nil))
		if an.Result == nil {
			continue
		}
		for _, f := range an.Result.Files {
			furi, ok := an.URI(f.Path)
			if !ok {
				continue
			}
			for _, u := range f.Units {
				for _, ref := range u.Refs {
					q := server.Query{Snap: an.Snap, URI: furi,
						Offset: ref.Span.Start}
					var want []server.Span
					for _, l := range an.References(q, true) {
						if l.URI == furi {
							want = append(want, l.Span)
						}
					}
					var got []server.Span
					for _, h := range an.Highlights(q) {
						got = append(got, h.Span)
						if h.Kind < 1 || h.Kind > 3 {
							t.Fatalf("kind %d", h.Kind)
						}
					}
					if !slices.Equal(got, want) {
						t.Fatalf("%s:%d: highlights %v, references %v",
							f.Path, ref.Span.Start, got, want)
					}
					n++
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("nothing checked")
	}
}

func apply(text []byte, es []server.Edit) []byte {
	es = slices.Clone(es)
	slices.SortFunc(es, func(a, b server.Edit) int {
		return b.Span.Start - a.Span.Start
	})
	out := slices.Clone(text)
	for _, e := range es {
		out = slices.Concat(out[:e.Span.Start], []byte(e.NewText),
			out[e.Span.End:])
	}
	return out
}

// TestLibrariesEdit checks that the edited gohard.yaml parses without
// diagnostics and maps the library, keeping the libraries it had.
func TestLibrariesEdit(t *testing.T) {
	for _, src := range []string{
		"",
		"standard: \"2019\"",
		"standard: \"2019\"\n",
		"libraries:\n  a: [x]\n",
		"libraries:\n    a: [x]\nstandard: \"2019\"\n",
		"libraries: # vendor\n  a: [x]\n",
		"libraries: {}\n",
		"libraries:",
		"standard: \"2019\"\nlibraries:\n\n  # c\n  a:\n    - x\n",
	} {
		ed, ok := LibrariesEdit([]byte(src), "ven: [v/src]")
		if !ok {
			t.Fatalf("%q: no edit", src)
		}
		got := apply([]byte(src), []server.Edit{ed})
		cfg, ds := config.Parse("gohard.yaml", got)
		if len(ds) > 0 {
			t.Fatalf("%q -> %q: %v", src, got, ds)
		}
		l, ok := cfg.Library("ven")
		if !ok || len(l.Folders) != 1 || l.Folders[0].Path != "v/src" {
			t.Fatalf("%q -> %q: %+v", src, got, cfg.Libraries)
		}
		if strings.Contains(src, "a:") {
			if _, ok := cfg.Library("a"); !ok {
				t.Fatalf("%q -> %q lost a", src, got)
			}
		}
	}
	if _, ok := LibrariesEdit([]byte("libraries: {a: [x]}\n"),
		"b: [y]"); ok {
		t.Fatal("a flow mapping is not rewritten")
	}
}

func codes(an *Analysis, u string) []diag.Code {
	var out []diag.Code
	for _, fd := range an.Diagnostics {
		if fd.URI == u {
			for _, d := range fd.Diagnostics {
				out = append(out, d.Code)
			}
		}
	}
	return out
}

// TestQuickFixesResolve checks that applying each quick fix removes
// the diagnostic it fixes: the edited snapshot analyzes without it.
func TestQuickFixesResolve(t *testing.T) {
	top := "entity t is end;\narchitecture a of t is\n" +
		"  signal s : unsigned(3 downto 0);\n  constant c : integer := ZERO;\n" +
		"begin\nend;\n"
	p := project{"defs.vhd": []byte(featPkg), "t.vhd": []byte(top)}
	an := analyze(t, snapshotOf(t, p, nil))
	for _, w := range []string{"unsigned", "ZERO"} {
		off := strings.Index(top, w)
		fixes := an.QuickFixes(uri("t.vhd"),
			server.Span{Start: off, End: off})
		if len(fixes) == 0 {
			t.Fatalf("no fix for %s: %v", w, codes(an, uri("t.vhd")))
		}
		for _, fx := range fixes {
			if len(fx.Edits) != 1 || fx.Edits[0].URI != uri("t.vhd") {
				t.Fatalf("%+v", fx)
			}
			q := project{"defs.vhd": []byte(featPkg),
				"t.vhd": apply([]byte(top), fx.Edits[0].Edits)}
			an2 := analyze(t, snapshotOf(t, q, nil))
			before := strings.Count(strings.Join(msgs(an, "t.vhd"), "\n"),
				w+" is not declared")
			after := strings.Count(strings.Join(msgs(an2, "t.vhd"), "\n"),
				w+" is not declared")
			if after >= before {
				t.Fatalf("%s: still undefined after %q:\n%s", w, fx.Title,
					q["t.vhd"])
			}
		}
	}
	// Unmapped library: the fix maps the folder declaring the unit.
	lp := project{
		"gohard.yaml": []byte("standard: \"2019\"\n"),
		"ven/src/pkg.vhd": []byte(
			"package vp is\n  constant K : integer := 1;\nend;\n",
		),
		"top.vhd": []byte("library ven;\nuse ven.vp.all;\n" +
			"entity e is end;\n"),
	}
	an = analyze(t, snapshotOf(t, lp, nil))
	fixes := an.QuickFixes(uri("top.vhd"), server.Span{Start: 9, End: 9})
	if len(fixes) != 1 || fixes[0].Edits[0].URI != uri("gohard.yaml") {
		t.Fatalf("library fix %+v (%v)", fixes, codes(an, uri("top.vhd")))
	}
	lp["gohard.yaml"] = apply(lp["gohard.yaml"], fixes[0].Edits[0].Edits)
	an = analyze(t, snapshotOf(t, lp, nil))
	if slices.Contains(codes(an, uri("top.vhd")), diag.LibraryUnmapped) {
		t.Fatalf("still unmapped with %q", lp["gohard.yaml"])
	}
}

func msgs(an *Analysis, rel string) []string {
	var out []string
	for _, fd := range an.Diagnostics {
		if fd.URI == uri(rel) {
			for _, d := range fd.Diagnostics {
				out = append(out, d.Message)
			}
		}
	}
	return out
}
