package ide

import (
	"bytes"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// incrSnippets are typed by TestIncrementalAnalyzer's edits.
var incrSnippets = []string{"x ", "signal zz9 : bit;\n",
	"constant k9 : integer := 3;\n", "procedure p9;\n", "-- c\n",
	"constant k8 : natural := -1;\n", "end;\n"}

// TestIncrementalAnalyzer (#53): one analyzer fed a random sequence of
// edited snapshots, each analysis reusing the one before, answers
// every snapshot exactly as a fresh analyzer does: the diagnostics
// (sema, the elaboration checks and lint), hovers, definitions and
// symbols, in as many slices; on every corpus design and NEORV32.
func TestIncrementalAnalyzer(t *testing.T) {
	ps := corpus(t)
	if !testing.Short() {
		ps["neorv32"] = neorv(t)
	}
	steps, reused := 0, 0
	for _, name := range slices.Sorted(maps.Keys(ps)) {
		p := maps.Clone(ps[name])
		var vhd []string
		for _, k := range slices.Sorted(maps.Keys(p)) {
			if strings.HasSuffix(k, ".vhd") {
				vhd = append(vhd, k)
			}
		}
		if len(vhd) == 0 {
			continue
		}
		limit := 1 << 30
		if name == "neorv32" {
			limit = 20
		}
		a := New(Options{ToolVersion: "test"})
		var prev *Analysis
		r := env.SplitMix64{State: uint64(len(name))*7 + 53}
		pick := func(n int) int { return int(r.Uint64() % uint64(n)) }
		for step := range 6 {
			f := vhd[pick(len(vhd))]
			text := p[f]
			var starts []int
			for i := range len(text) {
				if i == 0 || text[i-1] == '\n' {
					starts = append(starts, i)
				}
			}
			at := 0
			if len(starts) > 0 {
				at = starts[pick(len(starts))]
			}
			if k := pick(len(incrSnippets) + 1); k < len(incrSnippets) {
				p[f] = slices.Concat(text[:at], []byte(incrSnippets[k]),
					text[at:])
			} else if end := bytes.IndexByte(text[at:], '\n'); end >= 0 {
				p[f] = slices.Concat(text[:at], text[at+end+1:])
			}
			snap := snapshotOf(t, p, nil)
			got, n := run(a.Analysis(snap))
			want, nw := run(New(Options{ToolVersion: "test"}).
				Analysis(snap))
			if n != nw {
				t.Fatalf("%s step %d (%s): %d slices, want %d", name,
					step, f, n, nw)
			}
			if !reflect.DeepEqual(answers(got, limit),
				answers(want, limit)) {
				t.Fatalf("%s step %d (%s): the analysis that reused "+
					"the previous one differs from a fresh one", name,
					step, f)
			}
			steps++
			if prev != nil && prev.Result != nil && got.Result != nil &&
				len(prev.Result.Units) > 0 && slices.ContainsFunc(
				got.Result.Units, func(u *sema.Unit) bool {
					return slices.Contains(prev.Result.Units, u)
				}) {
				reused++
			}
			prev = got
		}
	}
	t.Logf("%d edits, %d reused units of the previous analysis", steps,
		reused)
	if reused < steps/4 {
		t.Fatalf("only %d of %d edits reused the previous analysis",
			reused, steps)
	}
}
