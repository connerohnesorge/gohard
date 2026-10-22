package corpus

import (
	"fmt"
	"testing"
)

// TestReleaseIsV11 pins the switch: since the v1.1 flip (#18, #27)
// v1.0 and v1.1 designs run against their expected outcome; later
// milestones are not reached.
func TestReleaseIsV11(t *testing.T) {
	if Release != "v1.1" {
		t.Fatalf("Release = %q, want v1.1 after the flip", Release)
	}
	for m, want := range map[string]bool{"v1.0": true, "v1.1": true,
		"v0.9": true, "v1.2": false, "v2.0": false, "": false} {
		if got := Reached(m); got != want {
			t.Errorf("Reached(%q) = %v, want %v", m, got, want)
		}
	}
}

// TestManifestRetiredKeys: a design with the retired expected_v1_0
// outcome, or of a milestone after Release, is a manifest error (no
// check would read it); the same design without them parses and runs
// against expected only.
func TestManifestRetiredKeys(t *testing.T) {
	const base = "designs:\n  - name: x\n    testbench: tb\n" +
		"    files: [{path: a.vhd}]\n" +
		"    expected: {exit: 0, status: clean}\n"
	m, err := ParseManifest([]byte(base + "    milestone: v1.1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rs := m.Designs[0].Runs(); len(rs) != 1 ||
		rs[0].Expected.Status != "clean" {
		t.Errorf("runs %+v", rs)
	}
	for _, bad := range []string{
		"    milestone: v1.1\n    expected_v1_0: {exit: 2}\n",
		"    milestone: v1.2\n",
		"    milestone: v2.0\n",
		"    milestone: 1.1\n",
		"",
	} {
		if _, err := ParseManifest([]byte(base + bad)); err == nil {
			t.Errorf("accepted %q", bad)
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
