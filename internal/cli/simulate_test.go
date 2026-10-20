package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestFindCandidate: matching is case-insensitive, a library prefix
// selects the bare entity, and only listed candidates ever match.
func TestFindCandidate(t *testing.T) {
	cands := []string{"top_tb", "sev_tb"}
	for _, n := range []string{"top_tb", "TOP_TB", "work.Top_Tb",
		"lib.top_tb"} {
		if got, ok := findCandidate(cands, n); !ok || got != "top_tb" {
			t.Errorf("findCandidate(%q) = %q, %v", n, got, ok)
		}
	}
	for _, n := range []string{"", "nope", "top", "top_tb.x"} {
		if got, ok := findCandidate(cands, n); ok {
			t.Errorf("findCandidate(%q) matched %q", n, got)
		}
	}
}

// TestDiffTraces: equal traces say so, and a difference names the
// first differing line with both sides.
func TestDiffTraces(t *testing.T) {
	run := func(old, cur string) string {
		var buf bytes.Buffer
		inv := &Invocation{stderr: &buf}
		var o []byte
		if old != "\x00" {
			o = []byte(old)
		}
		diffTraces(inv, "t.trace", o, []byte(cur))
		return inv.stderr.String()
	}
	if got := run("a\nb\n", "a\nb\n"); !strings.Contains(got,
		"matches") {
		t.Errorf("equal: %q", got)
	}
	if got := run("\x00", "a\n"); !strings.Contains(got, "no recorded") {
		t.Errorf("missing: %q", got)
	}
	got := run("a\nb\nc\n", "a\nx\nc\n")
	for _, w := range []string{"line 2", `-"b"`, `+"x"`} {
		if !strings.Contains(got, w) {
			t.Errorf("diff %q lacks %q", got, w)
		}
	}
	if got := run("a\n", "a\nb\n"); !strings.Contains(got, "line 2") {
		t.Errorf("longer: %q", got)
	}
}
