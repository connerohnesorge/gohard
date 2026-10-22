package corpus

import (
	"fmt"
	"testing"
)

// TestReleaseIsV10 pins the switch: v1.0 designs run, v1.1 designs run
// against expected_v1_0 until Stage 4 moves Release.
func TestReleaseIsV10(t *testing.T) {
	if Release != "v1.0" {
		t.Fatalf("Release = %q, want v1.0 until the v1.1 flip", Release)
	}
	for m, want := range map[string]bool{"v1.0": true, "v1.1": false,
		"v0.9": true, "v2.0": false, "": false} {
		if got := Reached(m); got != want {
			t.Errorf("Reached(%q) = %v, want %v", m, got, want)
		}
	}
}

// TestReachedOrder: reachedBy is the order of (major, minor) on
// well-formed milestones: reflexive, transitive, total, and it moves
// with the release; malformed milestones are never reached.
func TestReachedOrder(t *testing.T) {
	var ms []string
	for a := range 4 {
		for b := range 12 {
			ms = append(ms, fmt.Sprintf("v%d.%d", a, b))
		}
	}
	for i, x := range ms {
		if !reachedBy(x, x) {
			t.Errorf("%s not reached by itself", x)
		}
		for j, y := range ms {
			if reachedBy(x, y) != (i <= j) {
				t.Errorf("reachedBy(%s, %s) = %v", x, y, reachedBy(x, y))
			}
			if !reachedBy(x, y) && !reachedBy(y, x) {
				t.Errorf("%s and %s are not ordered", x, y)
			}
		}
	}
	for _, bad := range []string{"", "v1", "1.0", "v1.", "v.1", "v1.0.0",
		"v01.0", "v1.00", "v-1.0", "v1.+1", "V1.0", "v1.0 "} {
		for _, r := range ms {
			if reachedBy(bad, r) || reachedBy(r, bad) {
				t.Errorf("malformed %q compared with %s", bad, r)
			}
		}
	}
}
