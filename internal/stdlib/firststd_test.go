package stdlib

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/token"
)

var (
	update = flag.Bool("update", false,
		"rewrite firststd.tsv from the recorded reference sets, keeping "+
			"manual lines")
	ghdlSrc = flag.String("ghdl", "",
		"GHDL's VHDL source folder (lib/ghdl/src of the Nix shell's "+
			"ghdl): re-record testdata/ghdl/*.txt from it")
)

// refFile is one GHDL source file a reference set is read from.
type refFile struct{ lib, path string }

// refSets are the GHDL package sources of each standard (README.md,
// "First-standard tags"). GHDL ships the IEEE 1164-1993, 1076.3-1997
// and 1076.2-1996 texts for VHDL-93, the IEEE 1076-2008 texts for
// VHDL-2008, and 2019 versions of std.textio and std.env only.
var refSets = []struct {
	std   token.Standard
	files []refFile
}{
	{token.Std1993, []refFile{
		{"std", "std/v93/standard.vhdl"},
		{"std", "std/v93/textio.vhdl"},
		{"ieee", "ieee/v93/std_logic_1164.vhdl"},
		{"ieee", "ieee/v93/numeric_std.vhdl"},
		{"ieee", "ieee/v93/numeric_bit.vhdl"},
		{"ieee", "ieee/math_real.vhdl"},
		{"ieee", "ieee/math_complex.vhdl"},
	}},
	{token.Std2008, []refFile{
		{"std", "std/v08/standard.vhdl"},
		{"std", "std/v08/textio.vhdl"},
		{"std", "std/v08/env.vhdl"},
		{"ieee", "ieee2008/std_logic_1164.vhdl"},
		{"ieee", "ieee2008/std_logic_textio.vhdl"},
		{"ieee", "ieee2008/numeric_std.vhdl"},
		{"ieee", "ieee2008/numeric_bit.vhdl"},
		{"ieee", "ieee2008/numeric_std_unsigned.vhdl"},
		{"ieee", "ieee2008/numeric_bit_unsigned.vhdl"},
		{"ieee", "ieee2008/math_real.vhdl"},
		{"ieee", "ieee2008/math_complex.vhdl"},
		{"ieee", "ieee2008/fixed_float_types.vhdl"},
		{"ieee", "ieee2008/fixed_generic_pkg.vhdl"},
		{"ieee", "ieee2008/fixed_pkg.vhdl"},
		{"ieee", "ieee2008/float_generic_pkg.vhdl"},
		{"ieee", "ieee2008/float_pkg.vhdl"},
	}},
	{token.Std2019, []refFile{
		{"std", "std/v19/textio.vhdl"},
		{"std", "std/v19/env.vhdl"},
	}},
}

func refPath(std token.Standard) string {
	return filepath.Join("testdata", "ghdl", std.String()+".txt")
}

// sameType maps the type marks that name one base type in the 2019 text
// to one spelling, so a 1993 overload on SIGNED or STD_LOGIC_VECTOR
// matches the 2008 overload on UNRESOLVED_SIGNED or STD_ULOGIC_VECTOR it
// became (in 2008 the resolved types are subtypes of the unresolved
// ones, so one overload serves both).
var sameType = map[string]string{
	"UNRESOLVED_UNSIGNED": "UNSIGNED", "U_UNSIGNED": "UNSIGNED",
	"UNRESOLVED_SIGNED": "SIGNED", "U_SIGNED": "SIGNED",
	"STD_LOGIC_VECTOR": "STD_ULOGIC_VECTOR", "STD_LOGIC": "STD_ULOGIC",
	"UNRESOLVED_UFIXED": "UFIXED", "U_UFIXED": "UFIXED",
	"UNRESOLVED_SFIXED": "SFIXED", "U_SFIXED": "SFIXED",
	"UNRESOLVED_FLOAT": "FLOAT", "U_FLOAT": "FLOAT",
	"UNRESOLVED_FLOAT32": "FLOAT32", "U_FLOAT32": "FLOAT32",
	"UNRESOLVED_FLOAT64": "FLOAT64", "U_FLOAT64": "FLOAT64",
	"UNRESOLVED_FLOAT128": "FLOAT128", "U_FLOAT128": "FLOAT128",
}

// matchKey is the kind-free identity used to find a declaration in a
// reference set: a 1993 function that 2008 turned into an alias (as
// To_StdLogicVector) or a type that became a subtype (STD_LOGIC_VECTOR)
// keeps its first standard. Type marks lose their prefix and map
// through sameType.
func matchKey(d Decl) string {
	k := d.Library + "." + d.Unit + " " + d.Name
	if d.Signature == "" {
		return k
	}
	sig := strings.TrimSuffix(strings.TrimPrefix(d.Signature, "["), "]")
	var parts []string
	for _, w := range strings.Fields(strings.ReplaceAll(sig, ",", " ")) {
		if w == "RETURN" || w == "return" {
			parts = append(parts, "return")
			continue
		}
		if i := strings.LastIndexByte(w, '.'); i >= 0 {
			w = w[i+1:]
		}
		if s, ok := sameType[w]; ok {
			w = s
		}
		parts = append(parts, w)
	}
	return k + " [" + strings.Join(parts, " ") + "]"
}

func readRef(t *testing.T, std token.Standard) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(refPath(std))
	if err != nil {
		t.Fatalf("%v (record it with -ghdl)", err)
	}
	set := map[string]bool{}
	for line := range strings.Lines(string(raw)) {
		line = strings.TrimRight(line, "\r\n")
		if line != "" && !strings.HasPrefix(line, "#") {
			set[line] = true
		}
	}
	return set
}

// TestRecordGHDL re-records the reference sets from GHDL's sources
// (developer only: go test ./internal/stdlib -run TestRecordGHDL -ghdl
// DIR). CI reads the recorded files.
func TestRecordGHDL(t *testing.T) {
	if *ghdlSrc == "" {
		t.Skip("no -ghdl folder")
	}
	for _, rs := range refSets {
		set := map[string]bool{}
		for _, f := range rs.files {
			src, err := os.ReadFile(filepath.Join(*ghdlSrc,
				filepath.FromSlash(f.path)))
			if err != nil {
				t.Fatal(err)
			}
			ds := declsOf(f.lib, f.path, src, rs.std)
			if len(ds) == 0 {
				t.Fatalf("%s: no declarations", f.path)
			}
			for _, d := range ds {
				set[matchKey(d)] = true
			}
		}
		keys := slices.Sorted(func(yield func(string) bool) {
			for k := range set {
				if !yield(k) {
					return
				}
			}
		})
		var b strings.Builder
		b.WriteString("# Declarations of GHDL's VHDL-" + rs.std.String() +
			" std/ieee sources,\n# recorded by TestRecordGHDL; see " +
			"internal/stdlib/README.md.\n")
		for _, f := range rs.files {
			b.WriteString("# source: " + f.path + "\n")
		}
		for _, k := range keys {
			b.WriteString(k + "\n")
		}
		if err := os.MkdirAll(filepath.Dir(refPath(rs.std)),
			0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(refPath(rs.std), []byte(b.String()),
			0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// derive is the first-standard rule (README.md): the oldest reference
// set that has the declaration, else 2019. A package only ever in 2008
// or later puts all its declarations there.
func derive(d Decl, refs map[token.Standard]map[string]bool,
) (token.Standard, string) {
	k := matchKey(d)
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
	for _, rs := range refSets {
		refs[rs.std] = readRef(t, rs.std)
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
		have[matchKey(d)] = true
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
