package cli

import (
	"bytes"
	"context"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

// The #64 acceptance checks: gohard import of the manifests the pinned
// checkouts carry (OSVVM's osvvm.pro, an open-logic .core with its
// depend:, PoC's build.pro), then gohard check on the result.

const repoRoot = "../.."

// pinned returns the absolute path of the pinned checkout p (a
// repo-relative slash path), failing when it is not checked out.
func pinned(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join(repoRoot, filepath.FromSlash(p)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("%s is missing: run git submodule update --init %s", p,
			p)
	}
	return abs
}

// copyTree copies the folder src to dst, without .git.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// project makes dir a project root (a .git folder).
func projectRoot(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// gohard runs the command line args in dir and returns the exit code
// and the output.
func gohard(dir string, args ...string) (int, string) {
	var stdout, stderr bytes.Buffer
	e := realenv.New(realenv.Options{Stdout: &stdout, Stderr: &stderr,
		Dir: dir})
	code := Run(context.Background(), e, args)
	return code, stdout.String() + stderr.String()
}

// importedFiles returns the file entries of dir's gohard.yaml.
func importedFiles(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "gohard.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for l := range strings.Lines(string(b)) {
		if f, ok := strings.CutPrefix(strings.TrimSpace(l), "- "); ok {
			files = append(files, f)
		}
	}
	return files
}

// appendConfig adds text to dir's gohard.yaml.
func appendConfig(t *testing.T, dir, text string) {
	t.Helper()
	p := filepath.Join(dir, "gohard.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, text...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// noCache refuses to create any .gohard folder, so analyzing a
// committed project leaves it as it is.
type noCache struct{ env.FS }

func (f noCache) MkdirAll(name string, perm fs.FileMode) error {
	if strings.Contains(filepath.ToSlash(name), ".gohard") {
		return fs.ErrPermission
	}
	return f.FS.MkdirAll(name, perm)
}

// reached analyzes the project at dir as gohard check does and returns
// the units of library lib that the units of the project file file
// reach: their dependencies, the secondary units of every primary unit
// reached, and so on, as "kind name".
func reached(t *testing.T, dir, file, lib string) []string {
	t.Helper()
	var stderr bytes.Buffer
	e := realenv.New(realenv.Options{Stderr: &stderr, Dir: dir})
	e.FS = noCache{e.FS}
	p, uerr := parseArgs([]string{"check"})
	if uerr != nil {
		t.Fatal(uerr.problem)
	}
	inv := &Invocation{Ctx: context.Background(), Env: e,
		Command: p.cmd, values: p.values, stderr: &stderr}
	l := inv.LoadConfig()
	found := workspace.Discover(e.FS, workspace.Options{Root: l.Root,
		Dir: dir})
	srcs, _ := workspace.Read(e.Jobs, 0, e.FS, found.Files, 0)
	cfg, libs := libraryFolders(inv, l.Root, l.Config, found.Files, nil)
	res := analyzeSources(inv, &diag.Report{}, cfg, srcs, libs, l.Root)
	if res == nil {
		t.Fatalf("%s: nothing analyzed", dir)
	}
	var queue []*sema.Unit
	seen := map[*sema.Unit]bool{}
	add := func(u *sema.Unit) {
		if !seen[u] && !u.Duplicate {
			seen[u] = true
			queue = append(queue, u)
		}
	}
	for _, u := range res.Units {
		if u.File != nil && u.File.Path == file {
			add(u)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, d := range u.Deps {
			add(d)
		}
		if !u.Kind.Primary() {
			continue
		}
		for _, s := range res.Units {
			if !s.Kind.Primary() && s.Library == u.Library &&
				s.Of == u.Name {
				add(s)
			}
		}
	}
	var out []string
	for _, u := range res.Units {
		if seen[u] && string(u.Library) == lib {
			out = append(out, u.Kind.String()+" "+string(u.Name))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// osvvmFolderOnly are the OSVVM units the testbench of
// testdata/libsguide/osvvm reaches through that example's folder
// mapping (every file of the checkout, the first of two same-named
// units winning) but not through the imported osvvm.pro: package
// bodies of variants osvvm.pro does not analyze under the profile.
// MemoryPkg and the ScoreboardPkg_* packages are generic instances,
// which have no body; the folder mapping also reads the bodies of the
// non-generic deprecated/*_c.vhd variants. RandomPkg2019 is the
// bodiless deprecated/RandomPkg2019_c.vhd under VHDL-2008, where the
// folder mapping takes the VHDL-2019 RandomPkg2019.vhd and its body.
var osvvmFolderOnly = []string{
	"package body memorypkg", "package body randompkg2019",
	"package body scoreboardpkg_int", "package body scoreboardpkg_intv",
	"package body scoreboardpkg_signed", "package body scoreboardpkg_slv",
	"package body scoreboardpkg_unsigned",
}

// TestImportOSVVM: gohard import of OSVVM's osvvm.pro under the
// default profile writes a gohard.yaml with which gohard check passes,
// both for a testbench that uses OSVVM from outside the root (the
// layout of testdata/libsguide/osvvm) and for OSVVM's own files inside
// the root; and the testbench reaches the same OSVVM units as with the
// folder mapping of testdata/libsguide/osvvm, but for osvvmFolderOnly.
func TestImportOSVVM(t *testing.T) {
	osvvm := pinned(t, "testdata/libs/osvvm")
	guide := filepath.Join(repoRoot, "testdata", "libsguide", "osvvm")
	want := reached(t, guide, "src/tb.vhd", "osvvm")

	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	copyTree(t, osvvm, filepath.Join(dir, "osvvm"))
	copyTree(t, filepath.Join(guide, "src"), filepath.Join(proj, "src"))
	projectRoot(t, proj)
	code, out := gohard(proj, "import", "../osvvm/osvvm.pro")
	if code != 0 || !strings.Contains(out, "summary: 0 errors, "+
		"0 warnings, 0 hints, 47 files") {
		t.Fatalf("gohard import: exit %d\n%s", code, out)
	}
	if code, out := gohard(proj, "check"); code != 0 ||
		!strings.Contains(out, "summary: 0 errors, 0 warnings, 0 hints") {
		t.Errorf("gohard check: exit %d\n%s", code, out)
	}
	got := reached(t, proj, "src/tb.vhd", "osvvm")
	var onlyImport, onlyFolder []string
	for _, u := range got {
		if !slices.Contains(want, u) {
			onlyImport = append(onlyImport, u)
		}
	}
	for _, u := range want {
		if !slices.Contains(got, u) {
			onlyFolder = append(onlyFolder, u)
		}
	}
	if len(got) < 70 || len(onlyImport) > 0 ||
		!slices.Equal(onlyFolder, osvvmFolderOnly) {
		t.Errorf("the import reaches %d units, the folder mapping %d; "+
			"only the import: %q; only the folder mapping: %q",
			len(got), len(want), onlyImport, onlyFolder)
	}

	// OSVVM's own files, inside the root, so that their problems are
	// reported. The files osvvm.pro does not analyze (demo/, the
	// deprecated/ and vendor variants, the template) are context.
	root := filepath.Join(dir, "osvvm")
	projectRoot(t, root)
	if code, out := gohard(root, "import", "osvvm.pro"); code != 0 {
		t.Fatalf("gohard import: exit %d\n%s", code, out)
	}
	files := importedFiles(t, root)
	code, out = gohard(root, append([]string{"check"}, files...)...)
	if code != 0 || !strings.Contains(out, "summary: 0 errors, "+
		"0 warnings, 0 hints, 47 files") {
		t.Errorf("gohard check of OSVVM: exit %d\n%s", code, out)
	}

	// Under SetVHDLVersion 2019 the profile's Supports2019* values
	// select OSVVM's VHDL-2019 packages (RandomPkg2019.vhd, not
	// deprecated/RandomPkg2019_c.vhd), and they check as well.
	if err := os.Remove(filepath.Join(root, "gohard.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "osvvm2019.pro"),
		[]byte("SetVHDLVersion 2019\ninclude osvvm.pro\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := gohard(root, "import", "osvvm2019.pro"); code != 0 {
		t.Fatalf("gohard import: exit %d\n%s", code, out)
	}
	files = importedFiles(t, root)
	if !slices.Contains(files, "RandomPkg2019.vhd") ||
		slices.ContainsFunc(files, func(f string) bool {
			return strings.HasPrefix(f, "deprecated/")
		}) {
		t.Errorf("VHDL-2019 import: %v", files)
	}
	code, out = gohard(root, append([]string{"check"}, files...)...)
	if code != 0 || !strings.Contains(out, "summary: 0 errors, "+
		"0 warnings, 0 hints, 47 files") {
		t.Errorf("gohard check of OSVVM at VHDL-2019: exit %d\n%s", code,
			out)
	}
}

// checkErrors returns the errors gohard check printed in out, as
// "file: message" without the position, each counted under the first
// class that matches it, and fails for an error no class matches.
func checkErrors(t *testing.T, out string,
	classes map[string]*regexp.Regexp,
) map[string]int {
	t.Helper()
	line := regexp.MustCompile(`^([^:]+):\d+:\d+: (error\[.*)$`)
	got := map[string]int{}
	for l := range strings.Lines(out) {
		m := line.FindStringSubmatch(strings.TrimSuffix(l, "\n"))
		if m == nil {
			continue
		}
		e := m[1] + ": " + m[2]
		known := false
		for _, name := range slices.Sorted(maps.Keys(classes)) {
			if classes[name].MatchString(e) {
				got[name]++
				known = true
				break
			}
		}
		if !known {
			t.Errorf("unexpected error: %s", l)
		}
	}
	return got
}

// TestImportOpenLogic: gohard import of open-logic's axi core resolves
// its depend: on the base core among the .core files of the pinned
// checkout and imports 49 files. The gohard.yaml it writes does not
// check, a known gap of #64's criterion 2, for two reasons no .core
// file holds. Every open-logic core depends on the base core, whose
// files use Synopsys ieee.std_logic_misc, which the bundled ieee does
// not have (#33). And its RAMs declare shared variables of a
// non-protected type, which open-logic's sim/run.py analyzes with GHDL
// -frelaxed-rules and NVC --relaxed: lenient: true. The test pins those
// errors and no other, then adds both, std_logic_misc as a user
// library (NVC's copy, as #54's lint survey supplies it), and checks
// that gohard check passes.
func TestImportOpenLogic(t *testing.T) {
	olo := pinned(t, "testdata/open-logic")
	dir := t.TempDir()
	copyTree(t, filepath.Join(olo, "src"), filepath.Join(dir, "src"))
	projectRoot(t, dir)
	code, out := gohard(dir, "import", "src/axi/olo_axi_dev.core")
	if code != 0 || !strings.Contains(out, "wrote gohard.yaml: 49 files "+
		"in 1 library") {
		t.Fatalf("gohard import: exit %d\n%s", code, out)
	}
	// The base core's files come first; its non-VHDL files are hints.
	files := importedFiles(t, dir)
	if !strings.HasPrefix(files[0], "src/base/vhdl/") ||
		!strings.HasPrefix(files[len(files)-1], "src/axi/vhdl/") ||
		!strings.Contains(out, "src/base/olo_base_dev.core:63:9: "+
			"hint[import/ignored]: tcl/olo_base_constraints_amd.tcl "+
			"ignored: file type tclSource is not VHDL") {
		t.Errorf("gohard import:\n%s\n%v", out, files)
	}
	var misc []string
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(bytes.ToLower(b),
			[]byte("ieee.std_logic_misc")) {
			misc = append(misc, regexp.QuoteMeta(f))
		}
	}
	// The other cores' files under the root are context.
	code, out = gohard(dir, append([]string{"check"}, files...)...)
	if code != 1 || !strings.Contains(out, "49 files") {
		t.Fatalf("gohard check: exit %d\n%s", code, out)
	}
	miscFile := "^(" + strings.Join(misc, "|") + "): "
	got := checkErrors(t, out, map[string]*regexp.Regexp{
		"misc": regexp.MustCompile(miscFile + `error\[(library/` +
			`missing-unit\]: library ieee has no unit std_logic_misc|` +
			`sema/undefined\]: (or|and|xor)_reduce is not declared$)`),
		"shared": regexp.MustCompile(`^src/base/vhdl/olo_base_ram_` +
			`(sdp|sp|tdp)\.vhd: error\[sema/error\]: the type of a ` +
			`shared variable must be a protected type, not Data_t$`),
	})
	if len(misc) != 6 || got["misc"] != 11 || got["shared"] != 3 {
		t.Errorf("%d files use std_logic_misc; errors: %v\n%s",
			len(misc), got, out)
	}

	nvcMisc, err := os.ReadFile(filepath.Join(pinned(t, "testdata/nvc"),
		"lib", "synopsys", "std_logic_misc.vhd"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "synopsys"),
		0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "synopsys",
		"std_logic_misc.vhd"), pre2008.ReplaceAll(nvcMisc, nil),
		0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "gohard.yaml")
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte("libraries:\n"), []byte("libraries:\n"+
		"  ieee:\n    - synopsys/std_logic_misc.vhd\n"), 1)
	if err := os.WriteFile(cfg, append(b, "lenient: true\n"...),
		0o644); err != nil {
		t.Fatal(err)
	}
	code, out = gohard(dir, append([]string{"check"}, files...)...)
	if code != 0 || !strings.Contains(out, "summary: 0 errors, ") ||
		!strings.Contains(out, "49 files") {
		t.Errorf("gohard check with std_logic_misc and lenient: exit "+
			"%d\n%s", code, out)
	}
}

// pre2008 matches a VHDL-2019 conditional-analysis block for standards
// before 2008 in NVC's std_logic_misc, as in internal/lint's survey.
var pre2008 = regexp.MustCompile("(?s)`if VHDL_VERSION < \"2008\" " +
	"then\n.*?`end if\n")

// pinnedNested returns the absolute path of the submodule sub (a slash
// path) of the pinned checkout top, failing when it is not checked out:
// only a recursive checkout has it, and until then it is an empty
// folder.
func pinnedNested(t *testing.T, top, sub string) string {
	t.Helper()
	abs := filepath.Join(pinned(t, top), filepath.FromSlash(sub))
	if ents, err := os.ReadDir(abs); err != nil || len(ents) == 0 {
		t.Fatalf("%s/%s is missing: run git submodule update --init "+
			"--recursive %s", top, sub, top)
	}
	return abs
}

// pocLibs are the libraries of PoC's lib/OsvvmLibraries.pro that PoC's
// files use, in its order: the src/bus/axi4*/*_OSVVM packages use
// osvvm_axi4 (OSVVM-AXI4), which uses osvvm and osvvm_common. Each is
// a submodule nested in the PoC checkout.
var pocLibs = []string{"osvvm", "OSVVM-Common", "OSVVM-AXI4"}

// pocRegression is the build order of PoC's regression.tcl as one .pro
// script, which gohard import needs: it reads one project file, and
// regression.tcl is plain Tcl that builds lib/OsvvmLibraries.pro, then
// PoC.pro. The first lines are lib/OsvvmLibraries.pro as it runs in a
// recursive checkout, where [DirectoryExists OSVVM-AXI4] (which import
// does not evaluate) holds, limited to pocLibs. The libraries sit
// outside the project root, as in a project that uses them.
const pocRegression = `include ../lib/osvvm/osvvm.pro
include ../lib/OSVVM-Common
include ../lib/OSVVM-AXI4
include PoC.pro
`

// TestImportPoC: gohard import of PoC's src/build.pro (the build.pro
// PoC.pro includes, which sets library PoC and includes the other 33)
// under the default profile imports all 214 files, once PoC's own
// setup step has written tb/common/local_configuration.vhdl, which
// src/build.pro analyzes (regression.tcl runs WriteLocalConfiguration
// first). The gohard.yaml it writes does not check, a known gap of
// #64's criterion 3: two PoC packages use osvvm_axi4 (OSVVM-AXI4, a
// submodule nested in the PoC checkout), which regression.tcl builds
// before PoC.pro, and four files leave the range of others to an
// unconstrained context, which GHDL -frelaxed (set by PoC's
// tools/poc.tcl) and gohard's lenient: true accept. The test pins those
// errors and no other. Then it imports PoC's files after the OSVVM
// libraries in regression.tcl's order (pocRegression), with every
// script of the pinned checkouts unchanged, adds lenient: true, and
// checks that gohard check passes on PoC's files.
func TestImportPoC(t *testing.T) {
	poc := pinned(t, "testdata/poc")
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	for _, l := range pocLibs {
		copyTree(t, pinnedNested(t, "testdata/poc", "lib/"+l),
			filepath.Join(dir, "lib", l))
	}
	copyTree(t, filepath.Join(poc, "src"), filepath.Join(proj, "src"))
	// PoC's tb/common, where local_configuration.vhdl goes, and the
	// testbench of arith_Scaler: without an instance gohard check takes
	// it as a top level under default generics that make a positive
	// constant 0. The other 167 testbench files are left out: with
	// them this test peaks at 2.0 GB, not 0.7 GB.
	for _, d := range []string{"common", "arith/Scaler"} {
		copyTree(t, filepath.Join(poc, "tb", filepath.FromSlash(d)),
			filepath.Join(proj, "tb", filepath.FromSlash(d)))
	}
	projectRoot(t, proj)
	// What PoC's WriteLocalConfiguration (tools/poc.tcl) writes.
	if err := os.WriteFile(filepath.Join(proj, "tb", "common",
		"local_configuration.vhdl"), []byte("package local_configuration "+
		"is\n\tconstant LOCAL_PROJECT_DIR : string := \".\";\n"+
		"end package;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := gohard(proj, "import", "src/build.pro")
	if code != 0 || !strings.Contains(out, "wrote gohard.yaml: 214 "+
		"files in 1 library") {
		t.Fatalf("gohard import: exit %d\n%s", code, out)
	}
	// The only lines skipped are PoC's own disabled command.
	for l := range strings.Lines(out) {
		if strings.Contains(l, "hint[") &&
			!strings.Contains(l, "hint[import/ignored]: disabled ignored") {
			t.Errorf("unexpected hint: %s", l)
		}
	}
	pocFiles := importedFiles(t, proj)
	code, out = gohard(proj, append([]string{"check"}, pocFiles...)...)
	if code != 1 || !strings.Contains(out, " 214 files") {
		t.Fatalf("gohard check: exit %d\n%s", code, out)
	}
	got := checkErrors(t, out, map[string]*regexp.Regexp{
		"axi4": regexp.MustCompile(`^src/bus/axi4(lite)?/` +
			`axi4(lite)?_OSVVM\.pkg\.vhdl: error\[(library/unmapped\]: ` +
			`library (osvvm_axi4|OSVVM_AXI4) is not mapped|` +
			`sema/undefined\]: (Axi4|InitAxi4)(Lite)?Rec(Type)? is not ` +
			`declared$)`),
		"others": regexp.MustCompile(`: error\[sema/error\]: others ` +
			`needs an index range from the context, and ` +
			`STD_LOGIC_VECTOR is unconstrained$`),
	})
	if got["axi4"] != 14 || got["others"] != 8 {
		t.Errorf("errors: %v\n%s", got, out)
	}

	b, err := os.ReadFile(filepath.Join(poc, "PoC.pro"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "PoC.pro"), b,
		0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "regression.pro"),
		[]byte(pocRegression), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(proj, "gohard.yaml")); err != nil {
		t.Fatal(err)
	}
	code, out = gohard(proj, "import", "regression.pro")
	if code != 0 || !strings.Contains(out, "wrote gohard.yaml: 323 "+
		"files in 4 libraries") {
		t.Fatalf("gohard import: exit %d\n%s", code, out)
	}
	// Hints only for PoC's disabled command and in the libraries.
	for l := range strings.Lines(out) {
		if strings.Contains(l, "hint[") && !strings.HasPrefix(l, "../lib/") &&
			!strings.Contains(l, "hint[import/ignored]: disabled ignored") {
			t.Errorf("unexpected hint: %s", l)
		}
	}
	// The files under the root are PoC's, the same as before.
	var files []string
	for _, f := range importedFiles(t, proj) {
		if !strings.HasPrefix(f, "../") {
			files = append(files, f)
		}
	}
	if !slices.Equal(files, pocFiles) {
		t.Errorf("PoC's files: %d imported with the libraries, %d alone",
			len(files), len(pocFiles))
	}
	appendConfig(t, proj, "lenient: true\n")
	code, out = gohard(proj, append([]string{"check"}, files...)...)
	if code != 0 || !strings.Contains(out, "summary: 0 errors, ") ||
		!strings.Contains(out, " 214 files") {
		t.Errorf("gohard check: exit %d\n%s", code, out)
	}
}
