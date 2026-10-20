package corpus

import (
	"bytes"
	"testing"

	"github.com/connerohnesorge/gohard/internal/elab"
)

// TestEndToEnd runs psl_simple and overload_ops through the sequential
// interpreter (elab.Interpret, PSL directives in internal/elab/psl) the
// way `gohard check --sim` does, and requires the recorded corpus.yaml
// outcome at seeds 0-3 with byte-identical traces.
func TestEndToEnd(t *testing.T) {
	m, read := loadManifest(t)
	for _, name := range []string{"psl_simple", "overload_ops"} {
		t.Run(name, func(t *testing.T) {
			d, ok := m.Design(name)
			if !ok {
				t.Fatalf("no design %s", name)
			}
			r := d.Runs()[0]
			src, err := r.Sources(read)
			if err != nil {
				t.Fatal(err)
			}
			var first []byte
			for seed := range uint64(4) {
				res, err := Execute(r, src, Options{Seed: seed,
					Code: elab.Interpret})
				if err != nil {
					t.Fatal(err)
				}
				if !res.Simulated {
					t.Fatalf("seed %d: not simulated: pending %v, %v",
						seed, res.Pending, res.Messages)
				}
				for _, p := range Check(r.Expected, res, MatchFor(r)) {
					t.Errorf("seed %d: %s", seed, p)
				}
				if seed == 0 {
					t.Logf("exit %d, stop %d fs, %d steps, %v", res.Exit,
						res.StopTime, res.Steps, res.Messages)
					first = res.Text
				} else if !bytes.Equal(res.Text, first) {
					t.Errorf("seed %d: trace differs from seed 0", seed)
				}
			}
		})
	}
}
