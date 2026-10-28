package sema

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/token"
	"go.yaml.in/yaml/v3"
)

// v11Design is the part of a corpus.yaml design the v1.1 checks read.
type v11Design struct {
	Name      string `yaml:"name"`
	Standard  string `yaml:"standard"`
	Milestone string `yaml:"milestone"`
	Config    string `yaml:"config"`
	Files     []struct{ Path string }
}

// TestV11CorpusClean: every milestone-v1.1 corpus design (prot_cond,
// gc_heap, psl_comments and the six #63 designs) analyzes under v1.1
// with no error, no opaque or unchecked unit, and exit 0 (#18, #20,
// #109, #63): every construct it uses is delivered.
func TestV11CorpusClean(t *testing.T) {
	b, err := os.ReadFile("../../testdata/corpus/corpus.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cf struct {
		Designs []v11Design `yaml:"designs"`
	}
	if err := yaml.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, d := range cf.Designs {
		if d.Milestone != "v1.1" {
			continue
		}
		n++
		dir := filepath.Join("../../testdata/corpus", d.Name)
		c := config.Defaults()
		if d.Config != "" {
			cb, err := os.ReadFile(filepath.Join(dir, d.Config))
			if err != nil {
				t.Fatal(err)
			}
			c, _ = config.Parse(d.Config, cb)
		}
		c.Standard = d.Standard
		var srcs []Source
		for _, f := range d.Files {
			text, err := os.ReadFile(filepath.Join(dir, f.Path))
			if err != nil {
				t.Fatal(err)
			}
			srcs = append(srcs, Source{Path: f.Path, Text: text})
		}
		r := Analyze(Input{Files: srcs, Config: c})
		for _, g := range r.Diagnostics {
			if g.Severity == diag.Error {
				t.Errorf("%s: %s:%d: %s: %s", d.Name, g.Path, g.Line,
					g.Code, g.Message)
			}
		}
		for _, u := range r.Units {
			if u.Opaque || u.Unchecked {
				t.Errorf("%s: %s is opaque or unchecked", d.Name,
					u.Label())
			}
		}
		exit := diag.ExitCode(r.Diagnostics, false, diag.NoSignal)
		if exit != 0 {
			t.Errorf("%s: exit %d, want 0", d.Name, exit)
		}
	}
	if n == 0 {
		t.Fatal("no milestone v1.1 design in the corpus")
	}
}

// readTree reads every .vhd file under dir as sources with paths
// relative to dir.
func readTree(t testing.TB, dir string) []Source {
	t.Helper()
	var srcs []Source
	err := filepath.WalkDir(dir, func(p string, e os.DirEntry,
		err error,
	) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".vhd") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		srcs = append(srcs, Source{Path: filepath.ToSlash(rel), Text: b})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return srcs
}

// v11Retired are the constructs the v1.0 fixture
// testdata/cli/check-unsupported-v11 reported as unsupported until
// v1.1, each alone in a file. v1.1 delivers them (Stage 3), so
// the fixture is retired; their simulation is covered by
// testdata/conformance/{protected_generics,names,psl_api}.
var v11Retired = map[string]string{
	"protected_generics.vhd": `package protected_generics is
  type t is protected
    generic (width : natural);
    procedure inc;
  end protected t;
end package protected_generics;
package body protected_generics is
  type t is protected body
    variable n : natural := 0;
    procedure inc is
    begin
      n := (n + 1) mod width;
    end procedure inc;
  end protected body t;
end package body protected_generics;
`,
	"env_psl.vhd": `entity env_psl is
end entity env_psl;

architecture sim of env_psl is
begin
  process
  begin
    assert not std.env.PslAssertFailed;
    std.env.SetPslCoverAssert(true);
    assert std.env.PslIsCovered and std.env.PslIsAssertCovered and
      std.env.GetPslCoverAssert;
    std.env.ClearPslState;
    wait;
  end process;
end architecture sim;
`,
	"path_name.vhd": `package path_name is
  type t is protected
    procedure show;
  end protected t;
end package path_name;

package body path_name is
  type t is protected body
    variable n : integer := 0;
    procedure show is
    begin
      report n'path_name & n'instance_name;
    end procedure show;
  end protected body t;
end package body path_name;
`,
}

// TestV11Retired: the constructs the v1.0 fixture reported as
// unsupported are analyzed under 2019 (no diagnostic, no opaque or
// unchecked unit, exit 0); under 2008 they never give
// unsupported/construct and no unit is opaque.
func TestV11Retired(t *testing.T) {
	for p, text := range v11Retired {
		files := map[string]string{p: text}
		r := analyze(t, files, func(c *config.Config) {
			c.Standard = "2019"
		})
		if got := diagText(r); got != "" {
			t.Errorf("2019: %s:\n%s", p, got)
		}
		for _, u := range r.Units {
			if u.Opaque || u.Unchecked {
				t.Errorf("2019: %s is opaque or unchecked", u.Label())
			}
		}
		r = analyze(t, files, nil)
		for _, d := range r.Diagnostics {
			if d.Code == diag.UnsupportedConstruct {
				t.Errorf("2008: %s:%d: %s", d.Path, d.Line, d.Message)
			}
		}
		for _, u := range r.Units {
			if u.Opaque {
				t.Errorf("2008: %s is opaque", u.Label())
			}
		}
	}
}

// TestEnvBundledImplemented: every VHDL-2019 declaration of the bundled
// std.env (subprograms, types, record elements, enumeration literals
// and implicit operations) is in an implemented #18 group, so no use
// of std.env is unsupported/construct under v1.1.
func TestEnvBundledImplemented(t *testing.T) {
	n := 0
	for _, u := range Bundle().Units {
		if u.Library != "std" || u.Name != "env" {
			continue
		}
		for _, d := range u.Decls {
			if d.First < token.Std2019 {
				continue
			}
			n++
			if g := bundledConstruct(d); g != "" {
				t.Errorf("std.env.%s: %s", d.Text, g)
			}
		}
	}
	if n < 40 {
		t.Errorf("%d VHDL-2019 std.env declarations", n)
	}
}

// flippedDir holds the delivered constructs of the v1.0 construct
// fixture, each now a check --sim conformance case.
const flippedDir = "../../testdata/conformance/v10_flipped"

// flippedLegacy are the flipped cases whose construct an older
// standard reports as syntax/standard, by file.
var flippedLegacy = map[string]string{
	"inferred.vhd":                    cInferred,
	"variable_port.vhd":               cVariablePort,
	"protected_param.vhd":             cProtParam,
	"protected_array.vhd":             cProtArray,
	"protected_array_constrained.vhd": cProtArray,
	"protected_record.vhd":            cProtRecord,
	"method_alias.vhd":                cMethodAlias,
}

// TestV11Delivered: the delivered constructs of the v1.0 fixture are
// analyzed under 2019 (no unsupported/construct, no opaque or
// unchecked unit, no error), and under 2008 each one that no parser
// or view rule catches is still syntax/standard naming the construct.
func TestV11Delivered(t *testing.T) {
	srcs := readTree(t, flippedDir)
	if len(srcs) < 18 {
		t.Fatalf("%d flipped cases", len(srcs))
	}
	for _, s := range srcs {
		c := config.Defaults()
		c.Standard = "2019"
		r := Analyze(Input{Files: []Source{s}, Config: c})
		for _, d := range r.Diagnostics {
			t.Errorf("2019: %s:%d: %s: %s", d.Path, d.Line, d.Code,
				d.Message)
		}
		for _, u := range r.Units {
			if u.Opaque || u.Unchecked {
				t.Errorf("2019: %s is opaque or unchecked", u.Label())
			}
		}
		name, ok := flippedLegacy[s.Path]
		if !ok {
			continue
		}
		r = Analyze(Input{Files: []Source{s}, Config: config.Defaults()})
		want := name + " requires VHDL-2019"
		found := false
		for _, d := range r.Diagnostics {
			if d.Code == diag.UnsupportedConstruct {
				t.Errorf("2008: %s:%d: %s", d.Path, d.Line, d.Message)
			}
			found = found || d.Code == diag.SyntaxStandard &&
				strings.HasPrefix(d.Message, want)
		}
		if !found {
			t.Errorf("2008: %s: no syntax/standard for %s:\n%s", s.Path,
				name, diagText(r))
		}
	}
	for p, name := range flippedLegacy {
		if !legacy[name] || !delivered[name] {
			t.Errorf("%s: %s is not a delivered legacy construct", p, name)
		}
	}
}

// TestDesignEntityGroups: a construct is reported once per design
// entity at its first use (two architectures of e: one diagnostic,
// both opaque); a configuration is its own design entity and gets its
// own diagnostic; a unit instantiating the opaque configuration is
// unchecked. The construct is the please-report variant. No construct
// of the table found by node kind is unimplemented since #66 delivered
// `use vunit`, so the test adds one for that node kind.
func TestDesignEntityGroups(t *testing.T) {
	saved := constructs
	constructs = append(slices.Clone(constructs), construct{
		kind: cst.KindVerificationUnitBinding,
		name: "verification unit bindings in configurations"})
	t.Cleanup(func() { constructs = saved })
	arch := func(name, vunit string) string {
		return "architecture " + name + " of e is\n" +
			"  component leaf is end component;\n" +
			"  for u1 : leaf use entity work.leaf;\n" +
			"    use vunit " + vunit + ";\n" +
			"  end for;\n" +
			"begin\n  u1 : component leaf;\nend;\n"
	}
	files := map[string]string{
		"a_ent.vhd": "entity leaf is end;\n" +
			"architecture rtl of leaf is begin end;\n" +
			"entity e is end;\n",
		"b_arch.vhd": arch("r1", "v1"),
		"c_arch.vhd": arch("r2", "v2"),
		"d_cfg.vhd": "configuration cfg of e is\n  for r1\n" +
			"    for u1 : leaf\n      use vunit v3;\n    end for;\n" +
			"  end for;\nend;\n",
		"e_user.vhd": "entity g is end;\narchitecture rtl of g is\n" +
			"begin\n  u : configuration work.cfg;\nend;\n",
	}
	const vu = "verification unit bindings in configurations is not " +
		"supported yet, please report\n"
	want := "b_arch.vhd:4:5: unsupported/construct: " + vu +
		"d_cfg.vhd:4:7: unsupported/construct: " + vu +
		"e_user.vhd:2:14: sema/unchecked-unit: work.g(rtl) was not " +
		"checked: it depends on opaque unit work.cfg\n"
	for _, std := range []string{"2008", "2019"} {
		r := analyze(t, files, func(c *config.Config) {
			c.Standard = std
		})
		if got := diagText(r); got != want {
			t.Errorf("%s: got\n%s\nwant\n%s", std, got, want)
		}
		for _, u := range r.Units {
			wantOpaque := u.Of == "e" || u.Name == "cfg"
			if u.Opaque != wantOpaque {
				t.Errorf("%s: %s opaque = %v", std, u.Label(), u.Opaque)
			}
		}
	}
}

// TestInt64: VHDL-2019 INTEGER is 64 bits, so values outside 32 bits
// are analyzed under 2019 (no unsupported/construct, no error); under
// 2008 they are out of range errors, never unsupported/construct.
func TestInt64(t *testing.T) {
	for _, decl := range []string{
		"constant a : integer := 2 ** 31 - 1;",
		"constant a : integer := 2 ** 31;",
		"constant a : integer := 16#1_0000_0000#;",
		"constant a : natural := 3_000_000_000;",
		"constant a : boolean := integer'high = 9223372036854775807;",
		"constant a : integer := 1000 * 1000 * 1000 * 10;",
		"type big is range 0 to 2 ** 40;",
	} {
		files := map[string]string{"p.vhd": "package p is\n  " + decl +
			"\nend;\n"}
		r := analyze(t, files, func(c *config.Config) {
			c.Standard = "2019"
		})
		if got := diagText(r); got != "" {
			t.Errorf("2019: %s:\n%s", decl, got)
		}
		r = analyze(t, files, nil)
		if strings.Contains(diagText(r), "unsupported/construct") {
			t.Errorf("2008: %s:\n%s", decl, diagText(r))
		}
	}
}

// TestStandIn: the #20 stand-in rule. allow_opaque plus a
// simulation.models stand-in: the hint names the stand-in, dependents
// are checked against the stand-in (they resolve to it) and the run
// exits 0. Without allow_opaque the unit stays opaque and dependents
// are unchecked; without a stand-in, allow_opaque leaves dependents
// unchecked. The stand-in file is never a duplicate unit.
func TestStandIn(t *testing.T) {
	srcs := readTree(t, "../../testdata/cli/check-encrypted-standin/in")
	files := map[string]string{}
	for _, s := range srcs {
		files[s.Path] = string(s.Text)
	}
	model := []config.Model{{Unit: "work.core",
		Path: "models/core_model.vhd"}}
	run := func(allow bool, models []config.Model) *Result {
		return analyze(t, files, func(c *config.Config) {
			if allow {
				c.AllowOpaque = []string{"work.core"}
			}
			c.Simulation.Models = models
		})
	}
	exit := func(r *Result) int {
		return diag.ExitCode(r.Diagnostics, false, diag.NoSignal)
	}
	r := run(true, model)
	if len(r.Diagnostics) != 1 || r.Diagnostics[0].Code !=
		diag.SemaOpaqueAllowed || len(r.Diagnostics[0].Related) != 1 ||
		!strings.Contains(r.Diagnostics[0].Related[0].Message,
			"stand-in in models/core_model.vhd") || exit(r) != 0 {
		t.Fatalf("allowed with stand-in:\n%s", diagText(r))
	}
	off := strings.Index(files["top.vhd"], "work.core") + len("work.")
	ref, ok := r.RefAt("top.vhd", off)
	if !ok || ref.Decl == nil || ref.Decl.Path() !=
		"models/core_model.vhd" {
		t.Errorf("top does not resolve core to the stand-in: %+v", ref)
	}
	for _, u := range r.Units {
		if u.Unchecked {
			t.Errorf("%s is unchecked", u.Label())
		}
	}

	r = run(false, model)
	got := diagText(r)
	if !strings.Contains(got, "sema/encrypted-unit") ||
		!strings.Contains(got, "work.top(rtl) was not checked: it "+
			"depends on opaque unit work.core") ||
		strings.Contains(got, "duplicate") || exit(r) != diag.ExitTool {
		t.Errorf("stand-in without allow_opaque:\n%s", got)
	}

	r = run(true, nil)
	got = diagText(r)
	if !strings.Contains(got, "sema/opaque-allowed") ||
		!strings.Contains(got, "sema/unchecked-unit") ||
		!strings.Contains(got, "library/duplicate-unit") ||
		exit(r) != diag.ExitProblems {
		t.Errorf("allow_opaque without simulation.models:\n%s", got)
	}
}

// TestUnmappedFix: library/unmapped carries the quick fix pointing at
// the workspace folder declaring the units used, LibraryFixAt returns
// the same fix for the editor, a folder named like the library is the
// fallback, and no fix maps a folder holding the referencing file.
func TestUnmappedFix(t *testing.T) {
	files := map[string]string{
		"ip/nowhere/rtl/npkg.vhd":   "package npkg is end;",
		"ip/nowhere/sim/nsim.vhd":   "package nsim is end;",
		"third_party/vendorx/v.vhd": "entity v is end;",
		"src/top.vhd": "library nowhere, vendorx, gone;\n" +
			"use nowhere.npkg.all, nowhere.nsim.all;\n" +
			"entity top is end;\n",
		"src/self.vhd": "library mine;\nuse mine.mpkg.all;\n" +
			"entity self is end;\n",
		"src/mpkg.vhd": "package mpkg is end;",
	}
	r := analyze(t, files, nil)
	got := diagText(r)
	for _, w := range []string{
		"src/top.vhd:1:9: library/unmapped: library nowhere is not mapped",
		"src/top.vhd:1:18: library/unmapped: library vendorx",
		"src/top.vhd:1:27: library/unmapped: library gone",
		"src/self.vhd:1:9: library/unmapped: library mine",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	notes := map[string]string{}
	for _, d := range r.Diagnostics {
		if d.Code != diag.LibraryUnmapped {
			continue
		}
		var msgs []string
		for _, rel := range d.Related {
			if rel.Path != "gohard.yaml" {
				t.Errorf("fix at %s", rel.Path)
			}
			msgs = append(msgs, rel.Message)
		}
		notes[d.Message[:strings.Index(d.Message, " is")]] =
			strings.Join(msgs, "|")
	}
	want := map[string]string{
		"library nowhere": "quick fix: add `nowhere: [ip/nowhere]` " +
			"under libraries: (ip/nowhere declares nowhere.npkg, " +
			"nowhere.nsim)",
		"library vendorx": "quick fix: add `vendorx: " +
			"[third_party/vendorx]` under libraries: " +
			"(the folder is named like the library)",
		"library gone": "",
		"library mine": "",
	}
	for k, w := range want {
		if notes[k] != w {
			t.Errorf("%s: note %q, want %q", k, notes[k], w)
		}
	}
	fix, ok := r.LibraryFixAt("src/top.vhd", 10)
	if !ok || fix.Folder != "ip/nowhere" || fix.Entry !=
		"nowhere: [ip/nowhere]" || !slices.Equal(fix.Units,
		[]string{"nowhere.npkg", "nowhere.nsim"}) {
		t.Errorf("LibraryFixAt: %+v %v", fix, ok)
	}
	if _, ok := r.LibraryFixAt("src/top.vhd", 30); ok {
		t.Error("a fix for gone")
	}
	if _, ok := r.LibraryFixAt("src/top.vhd", 50); ok {
		t.Error("a fix off a library name")
	}
	// With libraries: set, the note points at that key.
	r = analyze(t, files, func(c *config.Config) {
		*c, _ = config.Parse("gohard.yaml",
			[]byte("libraries:\n  x: [third_party]\n"))
	})
	for _, d := range r.Diagnostics {
		if d.Code == diag.LibraryUnmapped && len(d.Related) > 0 &&
			d.Related[0].Line != 1 {
			t.Errorf("note at line %d", d.Related[0].Line)
		}
	}
}

// TestCommonDir checks the folder search helpers.
func TestCommonDir(t *testing.T) {
	for _, tc := range []struct {
		dirs []string
		want string
	}{
		{[]string{"a/b/c"}, "a/b/c"},
		{[]string{"a/b/c", "a/b/d"}, "a/b"},
		{[]string{"a/b", "ab/c"}, "."},
		{[]string{"x/y", "x/y/z"}, "x/y"},
	} {
		if got := commonDir(tc.dirs); got != tc.want {
			t.Errorf("commonDir(%v) = %q, want %q", tc.dirs, got,
				tc.want)
		}
	}
}

// TestOSVVM2019: OSVVM's VHDL-2019 builds of the assert API,
// file/line/path and language-support packages use the std.env
// features v1.1 delivers, so they are analyzed: no false semantic
// error and no unsupported/construct: every VHDL-2019 construct they
// use is delivered.
func TestOSVVM2019(t *testing.T) {
	root := "../../testdata/libs/osvvm"
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) == 0 {
		t.Skip("testdata/libs/osvvm is not checked out")
	}
	var srcs []Source
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".vhd") ||
			strings.Contains(n, "_Aldec") || strings.Contains(n, "_NVC") ||
			strings.Contains(n, "_template") ||
			strings.Contains(n, "MemoryPkgIs01") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, n))
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, Source{Path: "osvvm/" + n, Text: b})
	}
	c := config.Defaults()
	c.Standard = "2019"
	c.Libraries = []config.Library{{Name: "osvvm",
		Folders: []config.Folder{{Path: "osvvm"}}}}
	r := Analyze(Input{Files: srcs, Config: c})
	for _, d := range r.Diagnostics {
		t.Errorf("%s:%d: %s: %s", d.Path, d.Line, d.Code, d.Message)
	}
	checked := 0
	for _, u := range r.Units {
		if !u.Opaque && !u.Unchecked {
			checked++
		}
	}
	if checked == 0 {
		t.Errorf("no OSVVM unit is checked:\n%s", diagText(r))
	}
}
