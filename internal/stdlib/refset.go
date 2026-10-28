package stdlib

import (
	"errors"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// A reference set is the declaration set of another tool's std/ieee
// sources for one standard: GHDL's VHDL-93, 2008 and 2019 texts, from
// which the first-standard tags are derived, and NVC's VHDL-2008 text.
// The #33 Done-when compares the 2008 view with both 2008 sets. The
// sets are recorded by `go run ./tools/oracle --update stdlib` (the
// oracle's stdlib section reads the tools' sources from the Nix dev
// shell and the pinned NVC submodule) into testdata/<tool>/<std>.txt;
// the tests here read the recorded files, never the tools.

// RefSource is one source file of a reference set: the library it is
// compiled into and its slash path in the tool's source tree.
type RefSource struct{ Lib, Path string }

// RefSet describes one reference set.
type RefSet struct {
	// Tool is "ghdl" or "nvc".
	Tool string
	Std  token.Standard
	// Files are the sources, in order.
	Files []RefSource
}

// Name is the set's name in the oracle: "ghdl-2008".
func (s RefSet) Name() string { return s.Tool + "-" + s.Std.String() }

// Dir is the repository-relative folder of the recorded set and File
// its name in it.
func (s RefSet) Dir() string  { return "internal/stdlib/testdata/" + s.Tool }
func (s RefSet) File() string { return s.Std.String() + ".txt" }

// RefSets are every recorded reference set. GHDL (lib/ghdl/src of the
// Nix shell's ghdl) ships the IEEE 1164-1993, 1076.3-1997 and
// 1076.2-1996 texts for VHDL-93, the IEEE 1076-2008 texts for
// VHDL-2008, and 2019 versions of std.textio and std.env only. NVC
// ships its libraries compiled only, so its set is read from the
// sources it compiles them from (lib/std.08/deps.mk,
// lib/ieee.08/deps.mk) in the pinned NVC submodule at the tag of the
// Nix shell's nvc; math_real and math_complex are the shared 1076.2
// texts of lib/ieee.
var RefSets = []RefSet{
	{"ghdl", token.Std1993, []RefSource{
		{"std", "std/v93/standard.vhdl"},
		{"std", "std/v93/textio.vhdl"},
		{"ieee", "ieee/v93/std_logic_1164.vhdl"},
		{"ieee", "ieee/v93/numeric_std.vhdl"},
		{"ieee", "ieee/v93/numeric_bit.vhdl"},
		{"ieee", "ieee/math_real.vhdl"},
		{"ieee", "ieee/math_complex.vhdl"},
	}},
	{"ghdl", token.Std2008, []RefSource{
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
	{"ghdl", token.Std2019, []RefSource{
		{"std", "std/v19/textio.vhdl"},
		{"std", "std/v19/env.vhdl"},
	}},
	{"nvc", token.Std2008, []RefSource{
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
	}},
}

// RecordedBy is the provenance line every recorded set carries.
const RecordedBy = "# recorded by go run ./tools/oracle --update stdlib; " +
	"see internal/stdlib/README.md."

// RecordRefSet returns the recorded form of s: a header naming the tool
// and version, the provenance line and the sources, then the sorted
// match keys (MatchKey) of every declaration the sources make. read
// returns a source by its path in s.Files.
func RecordRefSet(s RefSet, version string,
	read func(path string) ([]byte, error)) ([]byte, error) {
	set := map[string]bool{}
	for _, f := range s.Files {
		src, err := read(f.Path)
		if err != nil {
			return nil, err
		}
		ds := declsOf(f.Lib, f.Path, src, s.Std)
		if len(ds) == 0 {
			return nil, errors.New(f.Path + ": no declarations")
		}
		for _, d := range ds {
			set[MatchKey(d)] = true
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString("# Declarations of " + strings.ToUpper(s.Tool) + " " +
		version + "'s VHDL-" + s.Std.String() + " std/ieee sources,\n")
	b.WriteString(RecordedBy + "\n")
	for _, f := range s.Files {
		b.WriteString("# source: " + f.Path + "\n")
	}
	for _, k := range keys {
		b.WriteString(k + "\n")
	}
	return []byte(b.String()), nil
}

// ParseRefSet reads a recorded set: its source paths (the "# source:"
// lines) and its keys.
func ParseRefSet(b []byte) (sources []string, keys map[string]bool) {
	keys = map[string]bool{}
	for line := range strings.Lines(string(b)) {
		line = strings.TrimRight(line, "\r\n")
		if p, ok := strings.CutPrefix(line, "# source: "); ok {
			sources = append(sources, p)
			continue
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			keys[line] = true
		}
	}
	return sources, keys
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

// MatchKey is the kind-free identity used to find a declaration in a
// reference set: a 1993 function that 2008 turned into an alias (as
// To_StdLogicVector) or a type that became a subtype (STD_LOGIC_VECTOR)
// keeps its first standard. Type marks lose their prefix and map
// through sameType.
func MatchKey(d Decl) string {
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
