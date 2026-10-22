package elab_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/connerohnesorge/gohard/internal/corpus"
	"github.com/connerohnesorge/gohard/internal/elab"
)

// interpDesigns are the corpus designs the sequential interpreter must
// run to their recorded outcome (corpus.yaml expected); the v1.1
// designs gc_heap and prot_cond since the v1.1 flip.
var interpDesigns = []string{"assert_fail", "budget_hang", "force_ext",
	"gc_heap", "hier_generate", "md_resolve", "mode_views",
	"overload_ops", "prot_cond", "textio_env", "wait_forms"}

func corpusRead(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join("../..", filepath.FromSlash(rel)))
}

func manifest(t *testing.T) *corpus.Manifest {
	t.Helper()
	src, err := corpusRead(corpus.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := corpus.ParseManifest(src)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestInterpCorpus runs the designs at seeds 0-3: the expected exit,
// status, stop time and messages, and byte-identical traces across
// seeds.
func TestInterpCorpus(t *testing.T) {
	m := manifest(t)
	for _, name := range interpDesigns {
		d, ok := m.Design(name)
		if !ok {
			t.Fatalf("no design %s", name)
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := d.Runs()[0]
			src, err := r.Sources(corpusRead)
			if err != nil {
				t.Fatal(err)
			}
			var first []byte
			for seed := range uint64(4) {
				res, err := corpus.Execute(r, src, corpus.Options{
					Seed: seed, Code: elab.Interpret})
				if err != nil {
					t.Fatal(err)
				}
				if len(res.Pending) > 0 {
					t.Fatalf("processes without code: %v", res.Pending)
				}
				for _, p := range corpus.Check(r.Expected, res,
					corpus.MatchFor(r)) {
					t.Errorf("seed %d: %s", seed, p)
				}
				if t.Failed() {
					for _, msg := range res.Messages {
						t.Logf("message: %s %s %s", msg.Severity,
							msg.Code, msg.Message)
					}
					return
				}
				if seed == 0 {
					first = res.Text
				} else if string(res.Text) != string(first) {
					t.Errorf("seed %d: the trace differs from seed 0",
						seed)
				}
			}
		})
	}
}
