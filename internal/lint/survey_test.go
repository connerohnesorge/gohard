package lint

import (
	"bytes"
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/corpus"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

var update = flag.Bool("update", false,
	"rewrite testdata/survey.golden from the current findings")

const root = "../.."

// surveySet is one body of real-world code lint is surveyed on (#54).
type surveySet struct {
	name string
	// load returns the analysis inputs, or ok false when the
	// submodule is not checked out.
	load func(t *testing.T) (srcs []sema.Source, cfg config.Config,
		ok bool)
}

// TestSurvey runs every rule (clock-edge-style included) on NEORV32,
// OSVVM and open-logic and on every corpus design, and compares the
// findings with testdata/survey.golden, the reviewed survey of #54:
// any new finding on these sources is a change to review, true or
// false. Lint must not crash, must stay within its step budget, and
// the large sets must analyze without errors, or their files would be
// skipped and the survey would prove nothing.
func TestSurvey(t *testing.T) {
	var got, missing []string
	for _, s := range []surveySet{
		{"neorv32", neorv32}, {"osvvm", osvvm},
		{"open-logic", openLogic},
	} {
		srcs, cfg, ok := s.load(t)
		if !ok {
			t.Logf("%s is not checked out", s.name)
			missing = append(missing, s.name+": ")
			continue
		}
		r := sema.Analyze(sema.Input{Files: srcs, Config: cfg})
		for _, d := range r.Diagnostics {
			if d.Severity == diag.Error {
				t.Fatalf("%s: %s", s.name, show(d))
			}
		}
		got = append(got, findings(t, s.name, r)...)
	}
	got = append(got, corpusFindings(t)...)

	path := filepath.Join("testdata", "survey.golden")
	text := []byte(strings.Join(got, "\n") + "\n")
	if *update {
		if len(missing) > 0 {
			t.Fatal("-update needs every submodule checked out")
		}
		if err := os.WriteFile(path, text, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	// Compare only the sets that are checked out.
	want = slices.DeleteFunc(want, func(l string) bool {
		return slices.ContainsFunc(missing, func(p string) bool {
			return strings.HasPrefix(l, p)
		})
	})
	if !slices.Equal(got, want) {
		t.Errorf("findings differ from %s (go test -run TestSurvey "+
			"-update rewrites it; review every change):\n%s", path,
			lineDiff(want, got))
	}
}

// findings lints r and returns its findings as "set: finding" lines,
// failing on a budget warning.
func findings(t *testing.T, set string, r *sema.Result) []string {
	t.Helper()
	var out []string
	for _, d := range Check(r, allOn) {
		if d.Code == diag.SemaEvalLimit {
			t.Errorf("%s: %s", set, show(d))
		}
		out = append(out, set+": "+show(d))
	}
	slices.Sort(out)
	return out
}

// corpusFindings lints every run of every corpus design.
func corpusFindings(t *testing.T) []string {
	t.Helper()
	read := func(rel string) ([]byte, error) {
		return os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	}
	b, err := read(corpus.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := corpus.ParseManifest(b)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range m.Designs {
		for _, run := range d.Runs() {
			src, err := run.Sources(read)
			if err != nil {
				t.Fatal(err)
			}
			r, _, err := run.Analyze(src)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, findings(t, "corpus/"+run.Name(), r)...)
		}
	}
	return out
}

// readSources reads the files of names (slash paths under dir) as
// sources named by those paths.
func readSources(t *testing.T, dir string, names []string,
) []sema.Source {
	t.Helper()
	var out []sema.Source
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(n)))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, sema.Source{Path: n, Text: b})
	}
	return out
}

// glob returns the slash paths under dir matching the patterns.
func glob(t *testing.T, dir string, patterns ...string) []string {
	t.Helper()
	var out []string
	for _, p := range patterns {
		ms, err := filepath.Glob(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ms {
			rel, _ := filepath.Rel(dir, m)
			out = append(out, filepath.ToSlash(rel))
		}
	}
	return out
}

// neorv32 is the core and testbench, as TestNEORV32 in internal/sema
// analyzes them.
func neorv32(t *testing.T) ([]sema.Source, config.Config, bool) {
	dir := filepath.Join(root, "testdata", "neorv32")
	names := glob(t, dir, "rtl/core/*.vhd", "sim/*.vhd")
	c := config.Defaults()
	c.Libraries = []config.Library{{Name: "neorv32",
		Folders: []config.Folder{{Path: "rtl/core"}, {Path: "sim"}}}}
	return readSources(t, dir, names), c, len(names) > 0
}

// osvvm is OSVVM as VHDL-2019 with the files its osvvm.pro selects, as
// TestOSVVM in internal/sema analyzes it.
func osvvm(t *testing.T) ([]sema.Source, config.Config, bool) {
	dir := filepath.Join(root, "testdata", "libs", "osvvm")
	var names []string
	for _, n := range glob(t, dir, "*.vhd") {
		if strings.Contains(n, "_Aldec") || strings.Contains(n, "_NVC") ||
			strings.Contains(n, "_template") ||
			strings.Contains(n, "MemoryPkgIs01") {
			continue
		}
		switch b := strings.TrimSuffix(n, ".vhd"); b {
		case "AssertApiPkg", "FileLinePathPkg", "LanguageSupport2019Pkg":
			n = "deprecated/" + b + "_c.vhd"
		}
		names = append(names, n)
	}
	c := config.Defaults()
	c.Standard = "2019"
	c.Libraries = []config.Library{{Name: "osvvm",
		Folders: []config.Folder{{Path: "."}, {Path: "deprecated"}}}}
	return readSources(t, dir, names), c, len(names) > 0
}

// openLogic is open-logic's compile_order.txt with two changes. The
// fix library (src/fix) is left out: it needs en_cl_fix, a submodule
// of open-logic that is not checked out. Synopsys std_logic_misc,
// which open-logic uses and the bundled ieee does not have (#33), is
// NVC's copy (testdata/nvc/lib/synopsys) with its pre-2008 branches
// removed, added to ieee. lenient: true allows the shared variables of
// a non-protected type that the RAMs use (GHDL -frelaxed).
func openLogic(t *testing.T) ([]sema.Source, config.Config, bool) {
	dir := filepath.Join(root, "testdata", "open-logic")
	order, err := os.ReadFile(filepath.Join(dir, "compile_order.txt"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, config.Config{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	misc, err := os.ReadFile(filepath.Join(root, "testdata", "nvc", "lib",
		"synopsys", "std_logic_misc.vhd"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, config.Config{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for l := range strings.Lines(string(order)) {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "src/") && !strings.HasPrefix(l, "src/fix/") {
			names = append(names, l)
		}
	}
	srcs := readSources(t, dir, names)
	srcs = append(srcs, sema.Source{Path: "synopsys/std_logic_misc.vhd",
		Text: pre2008.ReplaceAll(misc, nil)})
	c := config.Defaults()
	c.Lenient = true
	c.Libraries = []config.Library{{Name: "ieee",
		Folders: []config.Folder{{Path: "synopsys"}}}}
	return srcs, c, len(names) > 0
}

// pre2008 matches a VHDL-2019 conditional-analysis block for standards
// before 2008 in NVC's std_logic_misc.
var pre2008 = regexp.MustCompile("(?s)`if VHDL_VERSION < \"2008\" then\n" +
	".*?`end if\n")

// lineDiff lists the lines only in want (-) and only in got (+).
func lineDiff(want, got []string) string {
	var b bytes.Buffer
	for _, l := range want {
		if !slices.Contains(got, l) {
			b.WriteString("-" + l + "\n")
		}
	}
	for _, l := range got {
		if !slices.Contains(want, l) {
			b.WriteString("+" + l + "\n")
		}
	}
	return b.String()
}
