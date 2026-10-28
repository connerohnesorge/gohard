package ide

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/cache"
	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/server"
)

// diskCache returns an Options.Cache over a simulated file system and
// the stores it opened.
func diskCache() (func(string, config.Config) sema.Cache,
	*[]*cache.Store,
) {
	fs := simenv.New(simenv.Options{}).FS
	var opened []*cache.Store
	return func(root string, cfg config.Config) sema.Cache {
		st := cache.Open(fs, cache.Dir(root),
			cache.NewKey("test", sema.CacheSchema(), cfg))
		opened = append(opened, st)
		return st
	}, &opened
}

// answers are the analysis's diagnostics and its hover and definition
// answers at every name occurrence (at most limit per file).
func answers(an *Analysis, limit int) []any {
	out := []any{an.Diagnostics}
	for _, f := range an.Result.Files {
		u, ok := an.URI(f.Path)
		if !ok {
			continue
		}
		n := 0
		for _, unit := range f.Units {
			for _, ref := range unit.Refs {
				if n++; n > limit {
					break
				}
				q := server.Query{Snap: an.Snap, URI: u,
					Offset: ref.Span.Start}
				out = append(out, an.HoverAt(q), an.Definition(q))
			}
		}
		out = append(out, an.DocumentSymbols(u))
	}
	return out
}

// TestDiskCache (#37): the language server's analyses with the on-disk
// analysis cache, cold and then warm (a new analyzer, as after a
// restart), give the diagnostics, hovers, definitions and symbols of
// an analyzer without it, in as many slices, on every corpus design
// and OSVVM; the warm one restores every file and unit.
func TestDiskCache(t *testing.T) {
	ps := corpus(t)
	ps["osvvm"] = osvvm(t)
	for _, name := range slices.Sorted(maps.Keys(ps)) {
		t.Run(name, func(t *testing.T) {
			snap := snapshotOf(t, ps[name], nil)
			limit := 1 << 30
			if name == "osvvm" {
				limit = 20
			}
			want, nw := run(New(Options{ToolVersion: "test"}).
				Analysis(snap))
			opener, opened := diskCache()
			for _, stage := range []string{"cold", "warm"} {
				got, n := run(New(Options{ToolVersion: "test",
					Cache: opener}).Analysis(snap))
				if n != nw {
					t.Fatalf("%s: %d slices, want %d", stage, n, nw)
				}
				if !reflect.DeepEqual(answers(got, limit),
					answers(want, limit)) {
					t.Fatalf("%s: the answers differ", stage)
				}
			}
			st := (*opened)[len(*opened)-1].Stats()
			units := len(want.Result.Units)
			if files := len(want.Result.Files); st.Hits != files+units ||
				st.Puts != 0 {
				t.Fatalf("warm: %+v, want %d hits", st, files+units)
			}
		})
	}
}
