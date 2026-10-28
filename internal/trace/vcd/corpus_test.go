package vcd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// TestCorpusGoldensStable: every corpus golden trace exports to VCD
// bytes that are identical across exports and across a text round
// trip, start with a VCD header, and do not depend on the seed (#36).
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
		a := Export(tr)
		if !bytes.Equal(a, Export(tr)) {
			t.Fatalf("%s: VCD export not stable", f)
		}
		// The round trip is decoded once tr is no longer used, so one
		// decoded copy of budget_hang's 200,000 clock edges is live at
		// a time, not two.
		back, err := trace.Decode(trace.Encode(tr))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, Export(back)) {
			t.Fatalf("%s: VCD export not stable", f)
		}
		if !bytes.HasPrefix(a, []byte("$")) ||
			!bytes.Contains(a, []byte("$enddefinitions")) {
			t.Fatalf("%s: no VCD header", f)
		}
		back.Header.Seed ^= 0x5eed
		if !bytes.Equal(a, Export(back)) {
			t.Fatalf("%s: VCD export depends on the seed", f)
		}
	}
}
