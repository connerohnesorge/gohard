package ide

import (
	"maps"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/resolve"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/server"
)

// queryStats counts what checkQueries proved.
type queryStats struct {
	refs, defs, crossFile, crossLibrary, closed, symbols int
}

func contains(ls []server.Location, l server.Location) bool {
	return slices.Contains(ls, l)
}

// checkQueries checks the query layer's invariants on every name
// occurrence of the analysis:
//
//   - At at the start of an occurrence finds that occurrence and its
//     declaration;
//   - Definition there gives the declaration's location (a document of
//     the snapshot, possibly another file);
//   - ReferencesOf the declaration lists the occurrence;
//   - References is closed: asked at any listed location it gives the
//     same list (rename edits exactly this list), checked on every
//     stride-th declaration;
//   - document symbols lie in their document with the selection inside
//     the symbol.
func checkQueries(t *testing.T, an *Analysis, stride int) queryStats {
	t.Helper()
	var st queryStats
	r := an.Result
	seen := map[*sema.Decl]bool{}
	var decls []*sema.Decl
	for _, f := range r.Files {
		furi, ok := an.URI(f.Path)
		if !ok {
			t.Fatalf("no URI for %s", f.Path)
		}
		for _, u := range f.Units {
			for _, ref := range u.Refs {
				d := ref.Decl
				dl, ok := resolve.LocationOf(d)
				if d == nil || !ok || dl.Bundled || d.Implicit {
					continue
				}
				st.refs++
				q := server.Query{Snap: an.Snap, URI: furi,
					Offset: ref.Span.Start}
				tg, ok := an.At(q)
				if !ok || tg.Path != f.Path {
					t.Fatalf("%s:%d: At found nothing", f.Path,
						ref.Span.Start)
				}
				if !tg.IsRef || tg.Decl == nil {
					continue // a declaration sharing the position
				}
				want, ok := an.Location(dl)
				if !ok {
					t.Fatalf("no location for %s", dl.Path)
				}
				if tg.Span == Span(ref.Span) && tg.Decl == d {
					if def := an.Definition(q); !contains(def, want) {
						t.Fatalf("%s:%d %s: definition %v, want %v",
							f.Path, ref.Span.Start, d.Text, def, want)
					}
					st.defs++
				}
				self := server.Location{URI: furi, Span: Span(ref.Span)}
				if !contains(an.ReferencesOf(d, false), self) {
					t.Fatalf("%s:%d: references of %s miss it", f.Path,
						ref.Span.Start, d.Text)
				}
				if dl.Path != f.Path {
					st.crossFile++
				}
				if d.File.Library != f.Library {
					st.crossLibrary++
				}
				if !seen[d] {
					seen[d] = true
					decls = append(decls, d)
				}
			}
		}
		for _, s := range an.DocumentSymbols(furi) {
			checkSymbol(t, furi, s, &st)
		}
	}
	for i, d := range decls {
		if i%stride != 0 {
			continue
		}
		all := an.ReferencesOf(d, true)
		for _, l := range all {
			got := an.References(server.Query{Snap: an.Snap, URI: l.URI,
				Offset: l.Span.Start}, true)
			if !slices.Equal(got, all) {
				t.Fatalf("references of %s (%s) not closed at %v:\n"+
					"%v\n%v", d.Text, d.Kind, l, all, got)
			}
		}
		st.closed++
	}
	return st
}

func checkSymbol(t *testing.T, uri string, s server.Symbol,
	st *queryStats,
) {
	t.Helper()
	st.symbols++
	if s.URI != uri || s.Selection.Start < s.Span.Start ||
		s.Selection.End > s.Span.End || s.Name == "" {
		t.Fatalf("symbol %+v of %s", s, uri)
	}
	for _, c := range s.Children {
		checkSymbol(t, uri, c, st)
	}
}

// TestQueriesCorpus checks the query invariants on every corpus
// design, each with its first file open and edited (an open buffer
// over the disk files).
func TestQueriesCorpus(t *testing.T) {
	ps := corpus(t)
	var total queryStats
	for _, name := range slices.Sorted(maps.Keys(ps)) {
		t.Run(name, func(t *testing.T) {
			p := ps[name]
			var first string
			for _, k := range slices.Sorted(maps.Keys(p)) {
				if first == "" && len(k) > 4 && k[len(k)-4:] == ".vhd" {
					first = k
				}
			}
			open := map[string]string{first: "-- edited\n" +
				string(p[first])}
			st := checkQueries(t, analyze(t, snapshotOf(t, p, open)), 1)
			total.refs += st.refs
			total.crossFile += st.crossFile
			total.closed += st.closed
			total.defs += st.defs
			total.symbols += st.symbols
		})
	}
	if total.refs < 500 || total.crossFile < 50 ||
		total.defs*10 < total.refs*9 || total.symbols == 0 {
		t.Fatalf("too little coverage: %+v", total)
	}
	t.Logf("corpus: %+v", total)
}

func testLibrary(t *testing.T, p project, stride, minCross int) {
	an := analyze(t, snapshotOf(t, p, nil))
	st := checkQueries(t, an, stride)
	if st.crossFile < minCross || st.closed == 0 || st.symbols == 0 ||
		st.defs*10 < st.refs*9 {
		t.Fatalf("too little coverage: %+v", st)
	}
	t.Logf("%+v", st)
}

// TestQueriesOSVVM: the query invariants on OSVVM.
func TestQueriesOSVVM(t *testing.T) { testLibrary(t, osvvm(t), 7, 1000) }

// TestQueriesUVVM: the query invariants on UVVM across its three
// libraries.
func TestQueriesUVVM(t *testing.T) {
	p := uvvm(t)
	an := analyze(t, snapshotOf(t, p, nil))
	st := checkQueries(t, an, 7)
	if st.crossLibrary < 100 || st.crossFile < 1000 {
		t.Fatalf("too little coverage: %+v", st)
	}
	t.Logf("%+v", st)
}

// TestQueriesNEORV32: the query invariants on NEORV32.
func TestQueriesNEORV32(t *testing.T) {
	if testing.Short() {
		t.Skip("large project")
	}
	testLibrary(t, neorv(t), 11, 500)
}
