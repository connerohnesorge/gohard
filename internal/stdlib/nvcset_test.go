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

var nvcSrc = flag.String("nvc", "",
	"NVC's library source folder (lib of the pinned NVC regression "+
		"submodule, testdata/nvc/lib, which is the r1.23.0 tag of the "+
		"Nix shell's nvc): re-record testdata/nvc/2008.txt from it")

// nvcFiles are the sources NVC 1.23 compiles into its VHDL-2008 std
// and ieee libraries (lib/std.08/deps.mk, lib/ieee.08/deps.mk): NVC
// ships them compiled only, so the set is read from the same tag of
// its source tree. math_real and math_complex are the shared 1076.2
// texts of lib/ieee.
var nvcFiles = []refFile{
	{"std", "std.08/standard.vhd"},
	{"std", "std.08/textio.vhd"},
	{"std", "std.08/env.vhd"},
	{"ieee", "ieee.08/std_logic_1164.vhdl"},
	{"ieee", "ieee.08/std_logic_textio.vhdl"},
	{"ieee", "ieee.08/numeric_std.vhdl"},
	{"ieee", "ieee.08/numeric_bit.vhdl"},
	{"ieee", "ieee.08/numeric_std_unsigned.vhdl"},
	{"ieee", "ieee.08/numeric_bit_unsigned.vhdl"},
	{"ieee", "ieee/math_real.vhdl"},
	{"ieee", "ieee/math_complex.vhdl"},
	{"ieee", "ieee.08/fixed_float_types.vhdl"},
	{"ieee", "ieee.08/fixed_generic_pkg.vhdl"},
	{"ieee", "ieee.08/fixed_pkg.vhdl"},
	{"ieee", "ieee.08/float_generic_pkg.vhdl"},
	{"ieee", "ieee.08/float_pkg.vhdl"},
}

var nvcRefPath = filepath.Join("testdata", "nvc", "2008.txt")

// TestRecordNVC re-records NVC's VHDL-2008 declaration set (developer
// only: go test ./internal/stdlib -run TestRecordNVC -nvc
// ../../testdata/nvc/lib). CI reads the recorded file.
func TestRecordNVC(t *testing.T) {
	if *nvcSrc == "" {
		t.Skip("no -nvc folder")
	}
	set := map[string]bool{}
	for _, f := range nvcFiles {
		src, err := os.ReadFile(filepath.Join(*nvcSrc,
			filepath.FromSlash(f.path)))
		if err != nil {
			t.Fatal(err)
		}
		ds := declsOf(f.lib, f.path, src, token.Std2008)
		if len(ds) == 0 {
			t.Fatalf("%s: no declarations", f.path)
		}
		for _, d := range ds {
			set[matchKey(d)] = true
		}
	}
	var b strings.Builder
	b.WriteString("# Declarations of NVC 1.23's VHDL-2008 std/ieee " +
		"sources,\n# recorded by TestRecordNVC; see " +
		"internal/stdlib/README.md.\n")
	for _, f := range nvcFiles {
		b.WriteString("# source: " + f.path + "\n")
	}
	for _, k := range slices.Sorted(mapKeys(set)) {
		b.WriteString(k + "\n")
	}
	if err := os.MkdirAll(filepath.Dir(nvcRefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nvcRefPath, []byte(b.String()),
		0o644); err != nil {
		t.Fatal(err)
	}
}

// TestNVC2008Set: the 2008 view of ieee and std has exactly NVC's
// VHDL-2008 declaration set (#33 Done when), up to the reviewed
// differences in nvcLacks and nvcExtra (README.md).
func TestNVC2008Set(t *testing.T) {
	raw, err := os.ReadFile(nvcRefPath)
	if err != nil {
		t.Fatalf("%v (record it with -nvc)", err)
	}
	ref := map[string]bool{}
	for line := range strings.Lines(string(raw)) {
		line = strings.TrimRight(line, "\r\n")
		if line != "" && !strings.HasPrefix(line, "#") {
			ref[line] = true
		}
	}
	view := map[string]bool{}
	for _, d := range ViewOf(token.Std2008).Decls() {
		k := matchKey(d)
		view[k] = true
		if !ref[k] && !nvcLacks[k] {
			t.Errorf("view 2008 has %q; NVC 2008 does not", k)
		}
	}
	for _, k := range slices.Sorted(mapKeys(ref)) {
		if !view[k] && !nvcExtra[k] {
			t.Errorf("NVC 2008 has %q; view 2008 does not", k)
		}
	}
}

// nvcLacks are declarations of the 2008 view NVC's 2008 text does not
// make, and nvcExtra the reverse; each is reviewed in README.md.
// NVC's textio declares the attribute NEVER_WAITS, an NVC-private
// optimization hint on its read procedures that the LRM text does not
// have.
var (
	nvcLacks = map[string]bool{}
	nvcExtra = map[string]bool{"std.textio NEVER_WAITS": true}
)
