package stdlib

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/token"
)

var update = flag.Bool("update", false,
	"rewrite firststd.tsv from the recorded reference sets, keeping "+
		"manual lines")

// refFile returns the recorded reference set of tool and std, read by
// the tests from the oracle's recording (RefSets).
func refFile(tool string, std token.Standard) string {
	for _, rs := range RefSets {
		if rs.Tool == tool && rs.Std == std {
			return filepath.Join("testdata", rs.Tool, rs.File())
		}
	}
	panic("no reference set " + tool + " " + std.String())
}

// readSet reads a recorded reference set; each must carry the oracle's
// provenance line (#33: recorded by tools/oracle).
func readSet(t *testing.T, tool string, std token.Standard,
) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(refFile(tool, std))
	if err != nil {
		t.Fatalf("%v (record it with go run ./tools/oracle --update "+
			"stdlib)", err)
	}
	if !strings.Contains(string(raw), RecordedBy+"\n") {
		t.Fatalf("%s: not recorded by tools/oracle (rerun go run "+
			"./tools/oracle --update stdlib)", refFile(tool, std))
	}
	_, set := ParseRefSet(raw)
	return set
}

func readRef(t *testing.T, std token.Standard) map[string]bool {
	t.Helper()
	return readSet(t, "ghdl", std)
}

// derive is the first-standard rule (README.md): the oldest reference
// set that has the declaration, else 2019. A package only ever in 2008
// or later puts all its declarations there.
func derive(d Decl, refs map[token.Standard]map[string]bool,
) (token.Standard, string) {
	k := MatchKey(d)
	switch {
	case refs[token.Std1993][k]:
		return token.Std1993, "ghdl-93"
	case refs[token.Std2008][k]:
		return token.Std2008, "ghdl-08"
	case refs[token.Std2019][k]:
		return token.Std2019, "ghdl-19"
	}
	return token.Std2019, "new-19"
}

func readRefs(t *testing.T) map[token.Standard]map[string]bool {
	refs := map[token.Standard]map[string]bool{}
	for _, std := range []token.Standard{token.Std1993, token.Std2008,
		token.Std2019} {
		refs[std] = readRef(t, std)
	}
	return refs
}

// TestTags checks firststd.tsv: well formed, exactly one line per
// declaration in Decls order, and every derived line agrees with the
// recorded reference sets. Lines whose source starts with "manual"
// carry a reviewed exception and keep their tag (-update keeps them).
func TestTags(t *testing.T) {
	raw, err := os.ReadFile(tagsFile)
	if err != nil {
		t.Fatal(err)
	}
	tags, bad := parseTags(string(raw))
	if len(bad) > 0 {
		t.Fatalf("firststd.tsv: malformed lines %v", bad)
	}
	refs := readRefs(t)
	manual := map[string]Tag{}
	for _, tg := range tags {
		if strings.HasPrefix(tg.Source, "manual") {
			manual[tg.Key] = tg
		}
	}
	ds := untagged()
	var want []Tag
	seen := map[string]bool{}
	for _, d := range ds {
		k := d.Key()
		if seen[k] {
			t.Errorf("duplicate declaration key %q", k)
		}
		seen[k] = true
		if m, ok := manual[k]; ok {
			want = append(want, m)
			continue
		}
		std, src := derive(d, refs)
		want = append(want, Tag{First: std, Source: src, Key: k})
	}
	if *update {
		writeTags(t, want)
		return
	}
	for i := range max(len(want), len(tags)) {
		switch {
		case i >= len(tags):
			t.Fatalf("firststd.tsv: missing line for %q (run -update)",
				want[i].Key)
		case i >= len(want):
			t.Fatalf("firststd.tsv: stale line %q", tags[i].Key)
		case tags[i] != want[i]:
			t.Fatalf("firststd.tsv line for %q:\n got %v\nwant %v "+
				"(run -update)", want[i].Key, tags[i], want[i])
		}
	}
	for k := range manual {
		if !seen[k] {
			t.Errorf("manual line for unknown declaration %q", k)
		}
	}
}

func writeTags(t *testing.T, tags []Tag) {
	t.Helper()
	raw, _ := os.ReadFile(tagsFile)
	var b strings.Builder
	for line := range strings.Lines(string(raw)) {
		if !strings.HasPrefix(line, "#") {
			break
		}
		b.WriteString(line) // keep the header comment
	}
	for _, tg := range tags {
		b.WriteString(tg.First.String() + "\t" + tg.Source + "\t" +
			tg.Key + "\n")
	}
	if err := os.WriteFile(tagsFile, []byte(b.String()),
		0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRefSetsCovered: every declaration GHDL's 1993, 2008 and 2019
// sources make is bundled (the 2008 ieee declaration set matches GHDL's, #33),
// apart from the reviewed differences in refMissing.
func TestRefSetsCovered(t *testing.T) {
	refs := readRefs(t)
	have := map[string]bool{}
	for _, d := range untagged() {
		have[MatchKey(d)] = true
	}
	for _, std := range []token.Standard{token.Std1993, token.Std2008,
		token.Std2019} {
		for _, k := range slices.Sorted(mapKeys(refs[std])) {
			if !have[k] && !refMissing[k] {
				t.Errorf("GHDL %s declares %q; the bundle does not", std,
					k)
			}
		}
	}
}

// refMissing lists reference-set declarations the bundle does not have,
// each reviewed (README.md). GHDL's standard.vhdl is a listing it
// generates, which declares the anonymous universal types; the LRM text
// has them only as comments, and analysis predefines them (#34).
var refMissing = map[string]bool{
	"std.standard UNIVERSAL_INTEGER": true,
	"std.standard UNIVERSAL_REAL":    true,
}

func mapKeys(m map[string]bool) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
