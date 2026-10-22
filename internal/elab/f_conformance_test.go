package elab

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// fCases are builder F's conformance cases (testdata/conformance) with
// their testbench and the exact diagnostics of a run: until the
// integrator removes the unsupported/construct detectors, the cli
// conformance test skips them as pending, so this test runs them
// through sema and elab with the detectors bypassed (analyze19).
var fCases = []struct {
	file, top string
	want      []string
}{
	{"protected/variable_port.vhd", "vport_tb", []string{
		"hint sim/assert-note report note: a = 3, b = 2",
	}},
	{"protected/composites.vhd", "composites_tb", []string{
		"hint sim/assert-note report note: bank(2) = 25, sum = 65",
		"hint sim/assert-note report note: nest = 0 7 0 10",
		"hint sim/assert-note report note: local = 0 4, pr = 3 0",
		"hint sim/assert-note report note: ps(1) = 9, ps(0) is null: " +
			"true",
	}},
	{"protected/pointers.vhd", "pointers_tb", []string{
		"hint sim/assert-note report note: p = 6, r = 8",
		"hint sim/assert-note report note: q is not null: true",
	}},
	{"protected/private_variable.vhd", "private_tb", []string{
		"hint sim/assert-note report note: s1 = 20, s2 = 10",
	}},
	{"protected/params.vhd", "params_tb", []string{
		"hint sim/assert-note report note: bump = 13, peek = 18, " +
			"deref = 42",
		"hint sim/assert-note report note: fresh = 7",
	}},
	{"inferred/objects.vhd", "inferred_tb", []string{
		"hint sim/assert-note report note: inf'length = 5, str = 1 to 3",
		"hint sim/assert-note report note: wa = 2 x 8",
		"hint sim/assert-note report note: s'length = 4, w = 4 x 3, " +
			"r'right = 5",
		"hint sim/assert-note report note: s = 0101",
	}},
}

// TestFConformance runs builder F's conformance cases: each reports
// exactly the values NVC 1.23 reports (composites.vhd: the values the
// LRM gives, NVC rejects the types), twice with the same result.
func TestFConformance(t *testing.T) {
	for _, c := range fCases {
		t.Run(c.file, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("..", "..", "testdata",
				"conformance", c.file))
			if err != nil {
				t.Fatal(err)
			}
			if m := regexp.MustCompile(`(?m)^entity (\w+) is$`).
				FindAllStringSubmatch(string(src), -1); len(m) == 0 ||
				m[len(m)-1][1] != c.top {
				t.Fatalf("the last entity is not %s", c.top)
			}
			r, _ := simulate19(t, string(src), c.top)
			r.wantNotes(t, c.want...)
			again, _ := simulate19(t, string(src), c.top)
			if string(again.text) != string(r.text) {
				t.Error("a second run gives another trace")
			}
		})
	}
}
