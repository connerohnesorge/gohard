package stdlib

import (
	"embed"
	"path"
	"slices"
	"strings"
)

// sources holds the vendored IEEE-P1076 files, their license files and
// the data files of this package, exactly as committed (.gitattributes
// keeps git from converting line endings, so the hashes hold on every
// OS). README.md is embedded for its Source table (Upstream).
//
//go:embed std/*.vhdl ieee/*.vhdl LICENSE AUTHORS.md SHA256SUMS
//go:embed firststd.tsv README.md
var sources embed.FS

// File is one bundled VHDL source file.
type File struct {
	// Path is the slash path inside the bundle, such as
	// "ieee/numeric_std.vhdl"; its folder is the library.
	Path string
	// Library is the library the file is analyzed into: "std" or
	// "ieee".
	Library string
	// Unit is the lower-case name of the package the file declares, or
	// whose body it holds.
	Unit string
	// Body reports that the file holds a package body.
	Body bool
}

// manifest lists every bundled VHDL file in analysis order: every
// package a file names (use clauses, package instantiations, interface
// packages) is declared by an earlier file, and a body follows its
// declaration. TestManifest checks this against the sources.
var manifest = []File{
	{Path: "std/standard.vhdl", Library: "std", Unit: "standard"},
	{Path: "std/textio.vhdl", Library: "std", Unit: "textio"},
	{Path: "std/env.vhdl", Library: "std", Unit: "env"},
	{Path: "std/reflection.vhdl", Library: "std", Unit: "reflection"},
	{Path: "ieee/std_logic_1164.vhdl", Library: "ieee",
		Unit: "std_logic_1164"},
	{Path: "ieee/std_logic_1164-body.vhdl", Library: "ieee",
		Unit: "std_logic_1164", Body: true},
	{Path: "ieee/std_logic_textio.vhdl", Library: "ieee",
		Unit: "std_logic_textio"},
	{Path: "ieee/numeric_std.vhdl", Library: "ieee", Unit: "numeric_std"},
	{Path: "ieee/numeric_std-body.vhdl", Library: "ieee",
		Unit: "numeric_std", Body: true},
	{Path: "ieee/numeric_bit.vhdl", Library: "ieee", Unit: "numeric_bit"},
	{Path: "ieee/numeric_bit-body.vhdl", Library: "ieee",
		Unit: "numeric_bit", Body: true},
	{Path: "ieee/numeric_std_unsigned.vhdl", Library: "ieee",
		Unit: "numeric_std_unsigned"},
	{Path: "ieee/numeric_std_unsigned-body.vhdl", Library: "ieee",
		Unit: "numeric_std_unsigned", Body: true},
	{Path: "ieee/numeric_bit_unsigned.vhdl", Library: "ieee",
		Unit: "numeric_bit_unsigned"},
	{Path: "ieee/numeric_bit_unsigned-body.vhdl", Library: "ieee",
		Unit: "numeric_bit_unsigned", Body: true},
	{Path: "ieee/math_real.vhdl", Library: "ieee", Unit: "math_real"},
	{Path: "ieee/math_real-body.vhdl", Library: "ieee",
		Unit: "math_real", Body: true},
	{Path: "ieee/math_complex.vhdl", Library: "ieee",
		Unit: "math_complex"},
	{Path: "ieee/math_complex-body.vhdl", Library: "ieee",
		Unit: "math_complex", Body: true},
	{Path: "ieee/fixed_float_types.vhdl", Library: "ieee",
		Unit: "fixed_float_types"},
	{Path: "ieee/fixed_generic_pkg.vhdl", Library: "ieee",
		Unit: "fixed_generic_pkg"},
	{Path: "ieee/fixed_generic_pkg-body.vhdl", Library: "ieee",
		Unit: "fixed_generic_pkg", Body: true},
	{Path: "ieee/fixed_pkg.vhdl", Library: "ieee", Unit: "fixed_pkg"},
	{Path: "ieee/float_generic_pkg.vhdl", Library: "ieee",
		Unit: "float_generic_pkg"},
	{Path: "ieee/float_generic_pkg-body.vhdl", Library: "ieee",
		Unit: "float_generic_pkg", Body: true},
	{Path: "ieee/float_pkg.vhdl", Library: "ieee", Unit: "float_pkg"},
}

// Libraries are the bundled library names, sorted.
var Libraries = []string{"ieee", "std"}

// Files returns every bundled VHDL file in analysis order.
func Files() []File { return slices.Clone(manifest) }

// Lookup returns the bundled file at the slash path p (as in
// File.Path).
func Lookup(p string) (File, bool) {
	for _, f := range manifest {
		if f.Path == p {
			return f, true
		}
	}
	return File{}, false
}

// Source returns the embedded bytes of the bundled file at p (a
// File.Path, LICENSE or AUTHORS.md). The bytes are shared: callers must
// not modify them. Callers that need the bytes checked call Verify
// first, once per run.
func Source(p string) ([]byte, bool) {
	if !validName(p) {
		return nil, false
	}
	b, err := sources.ReadFile(p)
	return b, err == nil
}

// validName reports whether p is a clean relative slash path of a file
// that can be in the bundle.
func validName(p string) bool {
	return p != "" && path.Clean(p) == p && !strings.HasPrefix(p, "/") &&
		!strings.HasPrefix(p, "../")
}

// IsLibrary reports whether name (any case) is a bundled library.
func IsLibrary(name string) bool {
	return slices.Contains(Libraries, strings.ToLower(name))
}

// Upstream returns the upstream tag and commit of the vendored files,
// read from the Tag and Commit rows of the Source table in the embedded
// README.md, so the pin is written down once. Either is "" when the
// table lacks it; TestUpstream requires both.
func Upstream() (tag, commit string) {
	raw, err := sources.ReadFile("README.md")
	if err != nil {
		return "", ""
	}
	return parseUpstream(string(raw))
}

// parseUpstream reads the first code span of the "| Tag |" and
// "| Commit |" table rows.
func parseUpstream(s string) (tag, commit string) {
	for line := range strings.Lines(s) {
		cells := strings.Split(strings.TrimSpace(line), "|")
		if len(cells) < 3 || cells[0] != "" {
			continue
		}
		_, v, ok := strings.Cut(cells[2], "`")
		if !ok {
			continue
		}
		v, _, ok = strings.Cut(v, "`")
		if !ok {
			continue
		}
		switch strings.TrimSpace(cells[1]) {
		case "Tag":
			if tag == "" {
				tag = v
			}
		case "Commit":
			if commit == "" {
				commit = v
			}
		}
	}
	return tag, commit
}

// IEEE describes the bundled IEEE library as "<tag> <commit>", for
// `gohard version` and the repro header (#39), or "unknown" when the
// README table lacks either.
func IEEE() string {
	tag, commit := Upstream()
	if tag == "" || commit == "" {
		return "unknown"
	}
	return tag + " " + commit
}
