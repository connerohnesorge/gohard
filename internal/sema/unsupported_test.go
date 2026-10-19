package sema

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"go.yaml.in/yaml/v3"
)

// v11Design is the part of a corpus.yaml design the v1.1 checks read.
type v11Design struct {
	Name        string `yaml:"name"`
	Standard    string `yaml:"standard"`
	Milestone   string `yaml:"milestone"`
	Config      string `yaml:"config"`
	Files       []struct{ Path string }
	ExpectedV10 struct {
		Exit     int `yaml:"exit"`
		Messages []struct {
			Severity string `yaml:"severity"`
			Code     string `yaml:"code"`
			Path     string `yaml:"path"`
			Line     int    `yaml:"line"`
			Text     string `yaml:"text"`
		} `yaml:"messages"`
	} `yaml:"expected_v1_0"`
}

// TestV11CorpusExact: every milestone-v1.1 corpus design (prot_cond)
// gives exactly the diagnostics corpus.yaml records in expected_v1_0,
// in order, and its exit code (#18, #20). Only the listed messages may
// appear.
func TestV11CorpusExact(t *testing.T) {
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
		want := d.ExpectedV10.Messages
		if len(r.Diagnostics) != len(want) {
			t.Fatalf("%s: %d diagnostics, want %d:\n%s", d.Name,
				len(r.Diagnostics), len(want), diagText(r))
		}
		for i, w := range want {
			g := r.Diagnostics[i]
			if g.Severity.String() != w.Severity ||
				string(g.Code) != w.Code || g.Path != w.Path ||
				g.Line != w.Line || !strings.Contains(g.Message, w.Text) {
				t.Errorf("%s: diagnostic %d is %s:%d %s %s %q, want "+
					"%s:%d %s %s %q", d.Name, i, g.Path, g.Line,
					g.Severity, g.Code, g.Message, w.Path, w.Line,
					w.Severity, w.Code, w.Text)
			}
		}
		exit := diag.ExitCode(r.Diagnostics, false, diag.NoSignal)
		if exit != d.ExpectedV10.Exit {
			t.Errorf("%s: exit %d, want %d", d.Name, exit,
				d.ExpectedV10.Exit)
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

// v11Names are the v1.1 constructs of #18 the construct fixture holds,
// by file.
var v11Names = map[string]string{
	"private_variable.vhd":   cPrivateVariable,
	"protected_generics.vhd": cProtGenerics,
	"method_alias.vhd":       cMethodAlias,
	"conditional_return.vhd": cCondReturn,
	"call_generic_map.vhd":   cCallGenericMap,
	"reflection.vhd":         cReflection,
	"env_version.vhd":        cEnvVersion,
	"env_getenv.vhd":         cEnvGetenv,
	"env_file.vhd":           cEnvFile,
	"env_assert.vhd":         cEnvAssert,
	"env_time.vhd":           cEnvTime,
	"env_dir.vhd":            cEnvDir,
	"env_call_path.vhd":      cEnvCallPath,
	"inferred.vhd":           cInferred,
	"int64.vhd":              cInt64,
	"image_composite.vhd":    cImage,
	"path_name.vhd":          cPathName,
	"variable_port.vhd":      cVariablePort,
	"protected_array.vhd":    cProtArray,
	// A constrained array type is a subtype of an anonymous base.
	"protected_array_constrained.vhd": cProtArray,
	"protected_record.vhd":            cProtRecord,
	"protected_access.vhd":            cProtAccess,
	"protected_param.vhd":             cProtParam,
}

// TestV11Constructs: each v1.1 construct, alone in a file, gives exactly
// one unsupported/construct with the "planned for v1.1" message and
// makes its unit opaque; the plain protected type they use stays clean
// (testdata/cli/check-unsupported-v11, also a CLI fixture).
func TestV11Constructs(t *testing.T) {
	srcs := readTree(t, "../../testdata/cli/check-unsupported-v11/in")
	c := config.Defaults()
	c.Standard = "2019"
	r := Analyze(Input{Files: srcs, Config: c})
	got := map[string][]diag.Diagnostic{}
	for _, d := range r.Diagnostics {
		got[d.Path] = append(got[d.Path], d)
	}
	for _, s := range srcs {
		name, ok := v11Names[s.Path]
		ds := got[s.Path]
		if !ok {
			if len(ds) > 0 {
				t.Errorf("%s: unexpected %v", s.Path, ds)
			}
			continue
		}
		want := name + " is not supported yet (planned for v1.1)"
		if len(ds) != 1 || ds[0].Code != diag.UnsupportedConstruct ||
			ds[0].Message != want {
			t.Errorf("%s: got %v, want one %q", s.Path, ds, want)
		}
		f, _ := r.File(s.Path)
		opaque := false
		for _, u := range f.Units {
			opaque = opaque || u.Opaque
		}
		if !opaque {
			t.Errorf("%s: no unit is opaque", s.Path)
		}
	}
	if len(v11Names) != len(srcs)-1 {
		t.Errorf("%d construct files, %d names", len(srcs)-1,
			len(v11Names))
	}
	if diag.ExitCode(r.Diagnostics, false, diag.NoSignal) != diag.ExitTool {
		t.Error("v1.1 constructs do not exit 2")
	}
}

// TestV11BeforeStandard: under VHDL-2008 the same files never give
// unsupported/construct (the constructs are not legal there): the ones
// no parser or view rule catches are syntax/standard, and no unit is
// opaque.
func TestV11BeforeStandard(t *testing.T) {
	srcs := readTree(t, "../../testdata/cli/check-unsupported-v11/in")
	r := Analyze(Input{Files: srcs, Config: config.Defaults()})
	std := map[string]bool{}
	for _, d := range r.Diagnostics {
		if d.Code == diag.UnsupportedConstruct {
			t.Errorf("2008: %s:%d: %s", d.Path, d.Line, d.Message)
		}
		if d.Code == diag.SyntaxStandard {
			std[d.Path] = true
		}
	}
	for _, u := range r.Units {
		if u.Opaque {
			t.Errorf("2008: %s is opaque", u.Label())
		}
	}
	for p, name := range v11Names {
		if legacy[name] && !std[p] {
			t.Errorf("2008: %s: no syntax/standard for %s", p, name)
		}
	}
}

// TestDesignEntityGroups: a construct is reported once per design
// entity at its first use; the architecture of an opaque entity is
// opaque (no diagnostic, not unchecked); another entity using the
// same construct gets its own diagnostic.
func TestDesignEntityGroups(t *testing.T) {
	files := map[string]string{
		"a_ent.vhd": "use std.reflection.all;\nentity e is end;\n",
		"b_arch.vhd": "architecture rtl of e is\n" +
			"  signal s : bit_vector := \"01\";\n" +
			"  signal v : value_mirror;\nbegin end;\n",
		"c_other.vhd": "use std.reflection.all;\nentity f is end;\n" +
			"architecture rtl of f is begin end;\n",
		"d_user.vhd": "entity g is end;\narchitecture rtl of g is\n" +
			"begin\n  u : entity work.e;\nend;\n",
	}
	r := analyze(t, files, func(c *config.Config) { c.Standard = "2019" })
	got := diagText(r)
	want := "a_ent.vhd:1:9: unsupported/construct: std.reflection is " +
		"not supported yet (planned for v1.1)\n" +
		"b_arch.vhd:2:10: unsupported/construct: inferred constraints " +
		"is not supported yet (planned for v1.1)\n" +
		"c_other.vhd:1:9: unsupported/construct: std.reflection is " +
		"not supported yet (planned for v1.1)\n" +
		"d_user.vhd:2:14: sema/unchecked-unit: work.g(rtl) was not " +
		"checked: it depends on opaque unit work.e\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	for _, u := range r.Units {
		wantOpaque := u.Name == "e" || u.Of == "e" || u.Name == "f" ||
			u.Of == "f"
		if u.Opaque != wantOpaque {
			t.Errorf("%s opaque = %v", u.Label(), u.Opaque)
		}
	}
}

// TestInt64: only INTEGER values outside 32 bits are the v1.1 64-bit
// integers; universal values in other contexts and the 32-bit extremes
// are analyzed.
func TestInt64(t *testing.T) {
	for _, tc := range []struct {
		decl string
		want bool
	}{
		{"constant a : integer := 2 ** 31 - 1;", false},
		{"constant a : integer := -2147483648;", false},
		{"constant a : integer := -2 ** 31;", false},
		{"type big is range 0 to 2 ** 40;", false},
		{"constant a : real := 1.0e12;", false},
		{"constant a : integer := 2 ** 31;", true},
		{"constant a : integer := 16#1_0000_0000#;", true},
		{"constant a : natural := 3_000_000_000;", true},
		{"constant a : boolean := integer'high = 9223372036854775807;",
			true},
		{"constant a : integer := 1000 * 1000 * 1000 * 10;", true},
	} {
		files := map[string]string{"p.vhd": "package p is\n  " +
			tc.decl + "\nend;\n"}
		r := analyze(t, files, func(c *config.Config) {
			c.Standard = "2019"
		})
		got := strings.Contains(diagText(r), cInt64)
		if got != tc.want {
			t.Errorf("%s: 64-bit %v, want %v:\n%s", tc.decl, got,
				tc.want, diagText(r))
		}
	}
}

// TestFoldInt checks the 64-bit folding against direct arithmetic,
// including overflow refusals.
func TestFoldInt(t *testing.T) {
	for _, tc := range []struct {
		op   string
		vs   []int64
		want int64
		ok   bool
	}{
		{"+", []int64{1 << 62, 1 << 62}, 0, false},
		{"+", []int64{-5, 7}, 2, true},
		{"-", []int64{-1 << 63, 1}, 0, false},
		{"-", []int64{5, 7}, -2, true},
		{"*", []int64{1 << 32, 1 << 31}, 0, false},
		{"*", []int64{-3, 1 << 40}, -3 << 40, true},
		{"**", []int64{2, 62}, 1 << 62, true},
		{"**", []int64{2, 63}, 0, false},
		{"**", []int64{1, 1000}, 1, true},
		{"/", []int64{7, -2}, -3, true},
		{"/", []int64{7, 0}, 0, false},
	} {
		ops := map[string]eval.Op{"+": eval.OpAdd, "-": eval.OpSub,
			"*": eval.OpMul, "**": eval.OpPow, "/": eval.OpDiv}
		got, ok := applyInt(ops[tc.op], tc.vs)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("%d %s %d = %d %v, want %d %v", tc.vs[0], tc.op,
				tc.vs[1], got, ok, tc.want, tc.ok)
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

// TestOSVVM2019Opaque: OSVVM's VHDL-2019 builds of the assert API,
// file/line/path and language-support packages use std.env features of
// v1.1, so they are opaque, with only unsupported/construct (planned)
// and unchecked-unit diagnostics, never a false semantic error.
func TestOSVVM2019Opaque(t *testing.T) {
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
	unsupported := 0
	for _, d := range r.Diagnostics {
		switch {
		case d.Code == diag.UnsupportedConstruct &&
			strings.HasSuffix(d.Message, "(planned for v1.1)"):
			unsupported++
		case d.Code == diag.SemaUnchecked:
		default:
			t.Errorf("%s:%d: %s: %s", d.Path, d.Line, d.Code, d.Message)
		}
	}
	if unsupported == 0 {
		t.Errorf("no unsupported construct:\n%s", diagText(r))
	}
}
