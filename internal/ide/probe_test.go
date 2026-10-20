package ide

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/sema"
)

// TestProbe: on every corpus design the probe sees one analysis's
// phases in order (and none for a reused result), and the
// diagnostics equal those of an analyzer without a probe (#19).
func TestProbe(t *testing.T) {
	ps := corpus(t)
	want := []string{"parse", "units", "cross-file", "static", "done",
		"hierarchy", "analyzed"}
	for _, name := range slices.Sorted(maps.Keys(ps)) {
		t.Run(name, func(t *testing.T) {
			var got []string
			a := New(Options{ToolVersion: "test", Probe: &sema.Probe{
				Phase: func(n string) { got = append(got, n) }}})
			s := snapshotOf(t, ps[name], nil)
			an, _ := run(a.Analysis(s))
			plain, _ := run(New(Options{ToolVersion: "test"}).Analysis(s))
			if !reflect.DeepEqual(an.Diagnostics, plain.Diagnostics) {
				t.Fatal("the probe changed the diagnostics")
			}
			if !slices.Equal(got, want) {
				t.Fatalf("phases %v, want %v", got, want)
			}
			got = nil
			run(a.Analysis(snapshotOf(t, ps[name], nil)))
			if !slices.Equal(got, want[:1]) {
				t.Fatalf("reused result: phases %v", got)
			}
		})
	}
}
