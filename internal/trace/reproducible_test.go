package trace_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/trace"
	"github.com/connerohnesorge/gohard/internal/trace/tracetest"
)

// TestNonReproducibleLine: a --real-env trace carries the additive
// header line "reproducible no" right after end (#18); it round-trips,
// and a reproducible trace is byte for byte the v1.0 form.
func TestNonReproducibleLine(t *testing.T) {
	plain := trace.Encode(tracetest.Sample())
	if bytes.Contains(plain, []byte("reproducible")) {
		t.Fatal("a reproducible trace has the marker")
	}
	tr := tracetest.Sample()
	tr.Header.NonReproducible = true
	marked := trace.Encode(tr)
	lines := strings.SplitN(string(marked), "\n", 5)
	if lines[0] != trace.Magic || !strings.HasPrefix(lines[2], "end ") ||
		lines[3] != "reproducible no" {
		t.Fatalf("header lines %q", lines[:4])
	}
	if got := strings.Replace(string(marked), "reproducible no\n", "",
		1); got != string(plain) {
		t.Error("the marker changed another line")
	}
	back, err := trace.Decode(marked)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Header.NonReproducible {
		t.Error("Decode lost the marker")
	}
	if again := trace.Encode(back); !bytes.Equal(again, marked) {
		t.Error("marked trace not byte-stable")
	}
	for _, bad := range []string{"reproducible yes", "reproducible",
		"reproducible no no"} {
		in := strings.Replace(string(marked), "reproducible no", bad, 1)
		if _, err := trace.Decode([]byte(in)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// Only right after end.
	moved := strings.Replace(string(marked), "reproducible no\n", "", 1)
	moved = strings.Replace(moved, "testbench tb\n",
		"reproducible no\ntestbench tb\n", 1)
	if _, err := trace.Decode([]byte(moved)); err == nil {
		t.Error("marker before testbench accepted")
	}
}
