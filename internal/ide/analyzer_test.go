package ide

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/server"
)

func firstVHDL(p project) string {
	for _, k := range slices.Sorted(maps.Keys(p)) {
		if strings.HasSuffix(k, ".vhd") {
			return k
		}
	}
	return ""
}

// TestReuseChangesNothing: on every corpus design, an analyzer that
// analyzed an earlier snapshot (so it reuses parse trees and results)
// gives the same diagnostics in the same number of slices as a fresh
// one, re-parses only the edited file, and reuses a whole result for a
// snapshot with the same content.
func TestReuseChangesNothing(t *testing.T) {
	ps := corpus(t)
	for _, name := range slices.Sorted(maps.Keys(ps)) {
		t.Run(name, func(t *testing.T) {
			p := ps[name]
			f := firstVHDL(p)
			s1 := snapshotOf(t, p, nil)
			s2 := snapshotOf(t, p, map[string]string{
				f: string(p[f]) + "\nsignal broken\n"})
			a := New(Options{ToolVersion: "test"})
			an1, n1 := run(a.Analysis(s1))
			an2, n2 := run(a.Analysis(s2))
			fresh, nf := run(New(Options{ToolVersion: "test"}).Analysis(s2))
			if n2 != nf || !reflect.DeepEqual(an2.Diagnostics,
				fresh.Diagnostics) {
				t.Fatalf("reuse changed the analysis: %d vs %d slices",
					n2, nf)
			}
			for _, f1 := range an1.Result.Files {
				f2, _ := an2.Result.File(f1.Path)
				if same := f1.Parse == f2.Parse; same == (f1.Path == f) {
					t.Errorf("%s: tree reused %v", f1.Path, same)
				}
			}
			if len(an2.Diagnostics) == len(an1.Diagnostics) &&
				reflect.DeepEqual(an1.Diagnostics, an2.Diagnostics) {
				t.Error("the edit did not change the diagnostics")
			}
			// The same content again: the stored result, as many
			// slices.
			s3 := snapshotOf(t, p, nil)
			an3, n3 := run(a.Analysis(s3))
			if n3 != n1 || an3.Result != an1.Result || an3.Snap != s3 {
				t.Fatalf("no reuse for the same content (%d, %d slices)",
					n1, n3)
			}
		})
	}
}

// TestSlicesBounded: the parse slices of OSVVM each hold at most
// budget*BytesPerStep bytes of files (or one file), every slice before
// the last reports progress, and their number is the same with any
// job limit.
func TestSlicesBounded(t *testing.T) {
	snap := snapshotOf(t, osvvm(t), nil)
	a := New(Options{})
	const budget = 500
	p := a.plan(snap, budget)
	if len(p.chunks) < 10 {
		t.Fatalf("only %d parse slices", len(p.chunks)-1)
	}
	for i := range len(p.chunks) - 1 {
		n := 0
		for _, s := range p.srcs[p.chunks[i]:p.chunks[i+1]] {
			n += len(s.doc.Text())
		}
		last := len(p.srcs[p.chunks[i+1]-1].doc.Text())
		if n-last >= budget*BytesPerStep {
			t.Fatalf("slice %d parses %d bytes", i, n)
		}
	}
	var t0 server.Task[*Analysis] = a.Analysis(snap)
	slices := 0
	for {
		sl := t0.Run(budget)
		slices++
		if sl.Done || sl.Next == nil {
			break
		}
		if sl.Report == nil {
			t.Fatalf("slice %d reports no progress", slices)
		}
		t0 = sl.Next
	}
	if slices != len(p.chunks)+1 {
		t.Fatalf("%d slices, want %d", slices, len(p.chunks)+1)
	}
}

// TestJobsChangeNothing: the diagnostics do not depend on the job
// runner or its limit (#34, P1).
func TestJobsChangeNothing(t *testing.T) {
	snap := snapshotOf(t, uvvm(t), nil)
	seq, _ := run(New(Options{}).Analysis(snap))
	jobs := simenv.New(simenv.Options{Seed: 7}).Env().Jobs
	par, _ := run(New(Options{Jobs: jobs, Limit: 16}).Analysis(snap))
	if !reflect.DeepEqual(seq.Diagnostics, par.Diagnostics) {
		t.Fatal("diagnostics depend on the job runner")
	}
}

// TestDiagnosticsTask: the Diagnostics feature is the analysis's
// diagnostics, grouped by document URI in order, each file's list
// sorted, only for documents of the snapshot.
func TestDiagnosticsTask(t *testing.T) {
	p := corpus(t)["hier_generate"]
	snap := snapshotOf(t, p, nil)
	a := New(Options{})
	if !a.Features().Has(server.FeatureDiagnostics) {
		t.Fatal("no diagnostics feature")
	}
	fds, _ := run(a.Diagnostics(snap))
	if len(fds) == 0 {
		t.Fatal("hier_generate has an error file")
	}
	for i, fd := range fds {
		if _, ok := snap.Document(fd.URI); !ok {
			t.Fatalf("%s is not a document", fd.URI)
		}
		if i > 0 && fds[i-1].URI >= fd.URI {
			t.Fatal("not sorted by URI")
		}
		for _, d := range fd.Diagnostics {
			if uri(d.Path) != fd.URI || d.Line < 1 {
				t.Fatalf("%+v in %s", d, fd.URI)
			}
		}
	}
}

// TestNoVHDL: a snapshot without VHDL files has no result and no
// diagnostics, and queries find nothing.
func TestNoVHDL(t *testing.T) {
	snap := snapshotOf(t, project{"readme.txt": []byte("x")}, nil)
	an := analyze(t, snap)
	if an.Result != nil || len(an.Diagnostics) != 0 {
		t.Fatal("analysis of nothing")
	}
	q := server.Query{Snap: snap, URI: uri("readme.txt")}
	if _, ok := an.At(q); ok || an.Definition(q) != nil ||
		an.WorkspaceSymbols("") != nil {
		t.Fatal("queries found something")
	}
}
