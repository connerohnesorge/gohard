package corpus

import (
	"reflect"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// TestNativeBodiesCorpus is the corpus check of the native ieee bodies
// (#59, docs/native-bodies.md): every run of every corpus design (main
// and listed variants) at seeds 0..-corpus.seeds-1 gives the same
// trace bytes and the same diagnostics with the native bodies as with
// every ieee function interpreted from source. Pull requests run 4
// seeds, the nightly job 64 (scripts/ci/corpus-seeds.sh).
func TestNativeBodiesCorpus(t *testing.T) {
	m, read := loadManifest(t)
	for _, d := range m.Designs {
		if !selectedDesign(d.Name) {
			continue
		}
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()
			for _, r := range d.Runs() {
				src, err := r.Sources(read)
				if err != nil {
					t.Fatal(err)
				}
				for s := range *seeds {
					compareBodies(t, r, src, uint64(s))
				}
			}
		})
	}
}

func compareBodies(t *testing.T, r Run, src map[string][]byte,
	seed uint64,
) {
	t.Helper()
	run := func(interpreted bool) *Result {
		res, err := Execute(r, src, Options{Seed: seed,
			Code: interpreter(), InterpretedBodies: interpreted})
		if err != nil {
			t.Fatalf("%s seed %d (interpreted %v): %v", r.Name(), seed,
				interpreted, err)
		}
		return res
	}
	native, interp := run(false), run(true)
	if native.Simulated != interp.Simulated {
		t.Errorf("%s seed %d: simulated %v, interpreted %v", r.Name(),
			seed, native.Simulated, interp.Simulated)
	}
	if n, a, b, ok := trace.FirstDiff(interp.Text, native.Text); ok {
		t.Errorf("%s seed %d: native trace differs at line %d:\n"+
			"  interpreted: %s\n  native:      %s", r.Name(), seed, n, a,
			b)
	}
	if !reflect.DeepEqual(native.Messages, interp.Messages) ||
		native.Exit != interp.Exit || native.Status != interp.Status {
		t.Errorf("%s seed %d: native outcome %d %s %v, interpreted "+
			"%d %s %v", r.Name(), seed, native.Exit, native.Status,
			native.Messages, interp.Exit, interp.Status, interp.Messages)
	}
}
