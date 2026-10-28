package format

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// deepCall returns an assignment whose right side nests n calls of ten
// arguments each, all on one line: every level must be wrapped.
func deepCall(n int) string {
	args := strings.Repeat("aaaa, ", 10)
	return "x <= " + strings.Repeat("f("+args, n) + "y" +
		strings.Repeat(", z)", n) + ";\n"
}

// wrapVisits runs the wrap pass alone on src and returns the atoms the
// indenter walked during it and the number of atoms.
func wrapVisits(t *testing.T, src string) (visits, atoms int) {
	t.Helper()
	res := parser.Parse([]byte(src), parser.Options{
		Standard: token.Std2008})
	if !Parses(res) {
		t.Fatalf("input does not parse:\n%.200s", src)
	}
	d := build(res, Default().normalized())
	d.visits = 0
	wrapLines(d)
	return d.visits, len(d.atoms)
}

// TestWrapCostLocal: the wrap pass walks each line that needs no break
// once, however deep the wrapping elsewhere in the file. A pass that
// re-indents the whole file once per wrapping depth (seconds on a few
// deeply nested statements, #32 review) walks every short line again
// for every level.
func TestWrapCostLocal(t *testing.T) {
	short := strings.Repeat("y <= a;\n", 400)
	for _, n := range []int{1, 10, 60} {
		alone := "architecture a of e is begin\n" + deepCall(n) +
			"end;\n"
		mixed := "architecture a of e is begin\n" + short +
			deepCall(n) + short + "end;\n"
		v1, a1 := wrapVisits(t, alone)
		v2, a2 := wrapVisits(t, mixed)
		if v2-v1 != a2-a1 {
			t.Errorf("depth %d: %d short-line atoms walked %d times",
				n, a2-a1, v2-v1)
		}
	}
}

// TestWrapCostLinear: k copies of a deeply nested statement cost k
// times one copy: the statements are wrapped independently.
func TestWrapCostLinear(t *testing.T) {
	file := func(k int) string {
		return "architecture a of e is begin\n" +
			strings.Repeat(deepCall(40), k) + "end;\n"
	}
	v1, _ := wrapVisits(t, file(1))
	v2, _ := wrapVisits(t, file(2))
	step := v2 - v1
	for k := 3; k <= 8; k++ {
		vk, _ := wrapVisits(t, file(k))
		if vk != v1+(k-1)*step {
			t.Errorf("%d copies: %d visits, want %d", k, vk,
				v1+(k-1)*step)
		}
	}
}

// TestWrapDeepNesting: the deeply nested input wraps to a fixed point
// in which no line can be wrapped again, and keeps the invariants.
func TestWrapDeepNesting(t *testing.T) {
	src := "architecture a of e is begin\n" + deepCall(80) +
		deepCall(3) + "end;\n"
	popts := parser.Options{Standard: token.Std2008}
	for _, lim := range []int{40, 80} {
		opts := Default()
		opts.MaxLineLength = lim
		if err := checkInvariants([]byte(src), popts, opts); err != nil {
			t.Fatalf("max %d: %v", lim, err)
		}
	}
}
