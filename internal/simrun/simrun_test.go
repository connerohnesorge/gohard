package simrun

import (
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

func TestStopLevel(t *testing.T) {
	want := map[string]trace.Severity{"note": trace.Note,
		"warning": trace.Warning, "error": trace.Error,
		"failure": trace.Failure, "": trace.Error}
	for s, w := range want {
		if got := StopLevel(s); got != w {
			t.Errorf("StopLevel(%q) = %v, want %v", s, got, w)
		}
	}
}

// TestOrder: seed 0 is declaration order (nil), every other seed a
// seeded permutation.
func TestOrder(t *testing.T) {
	if Order(0) != nil {
		t.Error("seed 0 must be declaration order")
	}
	for _, s := range []uint64{1, 2, 1 << 63} {
		if Order(s) == nil {
			t.Errorf("seed %d gave declaration order", s)
		}
	}
}
