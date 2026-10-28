package fst

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/connerohnesorge/gohard/internal/trace"
)

// TestCorpusGoldensStable: every corpus golden trace exports to FST
// bytes that are identical across exports and across a text round
// trip, and the export decodes back to the trace's settled values
// (#36). Large goldens use the default block policy only.
func TestCorpusGoldensStable(t *testing.T) {
	files, err := filepath.Glob("../../../testdata/corpus/*/golden/trace.txt")
	if err != nil || len(files) < 13 {
		t.Fatalf("%d corpus goldens, err %v", len(files), err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := trace.Decode(data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		a, err := Exporter{}.Export(tr)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if c, _ := (Exporter{}).Export(tr); !bytes.Equal(a, c) {
			t.Fatalf("%s: FST export not stable", f)
		}
		check(t, tr, 0)
		if len(tr.Changes) < 5000 {
			check(t, tr, 3)
		}
		// The round trip is decoded once tr is no longer used, so one
		// decoded copy of budget_hang's 200,000 clock edges is live at
		// a time, not two.
		back, err := trace.Decode(trace.Encode(tr))
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := (Exporter{}).Export(back); !bytes.Equal(a, b) {
			t.Fatalf("%s: FST export not stable", f)
		}
		back.Header.Seed ^= 0x5eed
		if d, _ := (Exporter{}).Export(back); !bytes.Equal(a, d) {
			t.Fatalf("%s: FST export depends on the seed", f)
		}
	}
}
