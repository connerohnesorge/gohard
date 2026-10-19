package parser

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/token"
)

var regressionSet = flag.String("regression", "",
	"run the parser-regression gate (#31) on set pr or nightly: every "+
		"file not on a GHDL/NVC expected-failure list must parse "+
		"without syntax errors; empty: round-trip the pr set and only "+
		"log the syntax-error count")

// repoRoot is the repository root seen from this package.
const repoRoot = "../.."

// manifest is the part of testdata/corpus/corpus.yaml the regression
// runner reads.
type manifest struct {
	Designs []struct {
		Name     string
		Standard string
		Files    []manifestFile
		Variants []struct {
			Files []manifestFile
		}
		Models map[string]string
		Config string
	}
	Regression []regEntry
}

type manifestFile struct {
	Path     string
	Standard string
}

// regEntry is one `regression:` entry (testdata/corpus/README.md).
type regEntry struct {
	Name             string
	Path             string
	Tag              string
	Subfolder        string
	ExpectedFailures []string `yaml:"expected_failures"`
}

func readManifest(t testing.TB) manifest {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "corpus",
		"corpus.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("corpus.yaml: %v", err)
	}
	if len(m.Designs) != 14 || len(m.Regression) == 0 {
		t.Fatalf("corpus.yaml: %d designs, %d regression entries",
			len(m.Designs), len(m.Regression))
	}
	return m
}

// expectation is what the upstream project says about one file.
type expectation struct {
	// mayFail: the file is on an expected-failure list. It must still
	// round-trip and never crash the parser.
	mayFail bool
	// runs are the option sets under which the file must parse with
	// no syntax error, each with the reference that requires it.
	runs []run
	// anyStd: no standard is recorded; the file must parse cleanly
	// under one of 2008, 2019 and 1993.
	anyStd bool
}

type run struct {
	opts Options
	why  string
	// relaxed: syntax/standard does not count (GHDL's 93c).
	relaxed bool
}

// regSource is one set of files with their expectations.
type regSource struct {
	name string
	// dir is the repo-relative folder walked for .vhd/.vhdl files.
	dir string
	// exp maps repo-relative slash paths to expectations. Files under
	// dir that are not in exp follow unlisted.
	exp map[string]*expectation
	// unlisted is the expectation of files exp does not name: nil means
	// "no upstream expectation" (parsed and reported, not gated).
	unlisted *expectation
	// submodule is the submodule path, for the missing-checkout hint.
	submodule string
	// dangling are the references to files that do not exist (outputs
	// the scripts generate, such as syn_*.vhdl), sorted.
	dangling []string
}

func (s *regSource) get(file string) *expectation {
	e := s.exp[file]
	if e == nil {
		e = &expectation{}
		s.exp[file] = e
	}
	return e
}

// missingSubmodule is the failure for an absent or empty checkout: it
// never skips (#8, testdata/corpus/README.md).
func missingSubmodule(dir, submodule string, err error) error {
	return fmt.Errorf("%s: no VHDL files (%v); run git submodule update "+
		"--init %s", dir, err, submodule)
}

// regressionSources returns the sources of set ("pr" or "nightly"):
// the bundled std/ieee packages under internal/stdlib,
// the 14 corpus designs, and the regression entries of the set.
func regressionSources(t *testing.T, set string) []*regSource {
	t.Helper()
	m := readManifest(t)
	var out []*regSource
	stdlib := filepath.Join(repoRoot, "internal", "stdlib")
	if fi, err := os.Stat(stdlib); err == nil && fi.IsDir() {
		// Every bundled file must parse with no syntax error under
		// both 2008 and 2019 (#33): the bundled 2019 text serves every
		// standard's view.
		why := "bundled std/ieee (#33)"
		out = append(out, &regSource{name: "stdlib",
			dir: "internal/stdlib", exp: map[string]*expectation{},
			unlisted: &expectation{runs: []run{
				{opts: Options{Standard: token.Std2008}, why: why},
				{opts: Options{Standard: token.Std2019}, why: why},
			}}})
	}
	out = append(out, corpusSource(t, m))
	for _, e := range m.Regression {
		if e.Tag != "pr" && (set != "nightly" || e.Tag != "nightly") {
			continue
		}
		s, err := entrySource(e)
		if err != nil {
			t.Fatalf("regression entry %s: %v", e.Name, err)
		}
		out = append(out, s)
	}
	return out
}

// corpusSource: every corpus file under its standard from corpus.yaml
// and the design's gohard.yaml (defines, psl.*).
func corpusSource(t *testing.T, m manifest) *regSource {
	t.Helper()
	s := &regSource{name: "corpus", dir: "testdata/corpus",
		exp: map[string]*expectation{}, unlisted: &expectation{anyStd: true}}
	for _, d := range m.Designs {
		base := Options{}
		if d.Config != "" {
			src, err := os.ReadFile(filepath.Join(repoRoot, "testdata",
				"corpus", d.Name, filepath.FromSlash(d.Config)))
			if err != nil {
				t.Fatalf("%s: %v", d.Name, err)
			}
			c, ds := config.Parse(config.FileName, src)
			if len(ds) > 0 {
				t.Fatalf("%s config: %v", d.Name, ds)
			}
			base = FromConfig(c, "0.0.0-dev")
		}
		files := slices.Clone(d.Files)
		for _, v := range d.Variants {
			files = append(files, v.Files...)
		}
		for _, p := range sortedValues(d.Models) {
			files = append(files, manifestFile{Path: p,
				Standard: d.Standard})
		}
		for _, f := range files {
			name := path.Join(s.dir, d.Name, f.Path)
			e := s.get(name)
			if len(e.runs) > 0 {
				continue // first listing wins (variants repeat files)
			}
			std, ok := token.ParseStandard(f.Standard)
			if !ok {
				t.Fatalf("%s: standard %q", name, f.Standard)
			}
			o := base
			o.Standard = std
			e.runs = append(e.runs, run{opts: o,
				why: "corpus.yaml " + d.Name})
		}
	}
	return s
}

func sortedValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

// entrySource reads a regression entry's expected-failure files as
// corpus.yaml documents.
func entrySource(e regEntry) (*regSource, error) {
	sub := path.Clean(e.Subfolder)
	s := &regSource{name: e.Name, dir: path.Join(e.Path, sub),
		exp: map[string]*expectation{}, submodule: e.Path}
	module := filepath.Join(repoRoot, filepath.FromSlash(e.Path))
	root := filepath.Join(module, filepath.FromSlash(sub))
	if n, err := countVHDL(root); n == 0 {
		return nil, missingSubmodule(s.dir, e.Path, err)
	}
	if len(e.ExpectedFailures) == 0 {
		// The project keeps no expected-failure list: every file
		// must parse.
		s.unlisted = &expectation{anyStd: true}
		return s, nil
	}
	// rel turns a submodule-relative path into a subfolder-relative
	// one.
	rel := func(p string) (string, error) {
		if sub == "." {
			return p, nil
		}
		r, ok := strings.CutPrefix(p, sub+"/")
		if !ok {
			return "", fmt.Errorf("%s is outside subfolder %s", p, sub)
		}
		return r, nil
	}
	var (
		ghdl   []ghdlRef
		nvc    []nvcRef
		read   = map[string]bool{}
		isGHDL bool
	)
	for _, pat := range e.ExpectedFailures {
		matches, err := filepath.Glob(filepath.Join(module,
			filepath.FromSlash(pat)))
		if err != nil || len(matches) == 0 {
			return nil, fmt.Errorf("expected_failures %q matches no "+
				"file: stale corpus.yaml or missing submodule (run git "+
				"submodule update --init %s)", pat, e.Path)
		}
		slices.Sort(matches)
		for _, m := range matches {
			r, _ := filepath.Rel(module, m)
			p, err := rel(filepath.ToSlash(r))
			if err != nil {
				return nil, err
			}
			read[p] = true
			switch {
			case path.Base(p) == "testsuite.sh":
				isGHDL = true
				refs, err := readGHDLScript(root, p)
				if err != nil {
					return nil, err
				}
				ghdl = append(ghdl, refs...)
			case path.Ext(p) == ".exp":
				refs, err := readExp(root, p, token.Std1993, exactExpMode)
				if err != nil {
					return nil, err
				}
				ghdl = append(ghdl, refs...)
			case path.Base(p) == "testlist.txt":
				refs, err := readTestlist(root)
				if err != nil {
					return nil, err
				}
				nvc = append(nvc, refs...)
			case strings.HasPrefix(path.Base(p), "test_") &&
				path.Ext(p) == ".c":
				refs, err := readUnitTests(root, p)
				if err != nil {
					return nil, err
				}
				nvc = append(nvc, refs...)
			default:
				return nil, fmt.Errorf("expected_failures: no reader "+
					"for %s", p)
			}
		}
	}
	if isGHDL {
		// Files GHDL must analyse are also named by the scripts that
		// carry no failures (synth, vhpi, vpi, nested gna dirs) and by
		// the VESTS driver's compliant lists.
		more, err := ghdlMustParseRefs(root, read)
		if err != nil {
			return nil, err
		}
		ghdl = append(ghdl, more...)
	}
	sortRefs(ghdl)
	for _, r := range ghdl {
		name := path.Join(s.dir, r.file)
		if !fileExists(name) {
			s.dangling = append(s.dangling, name)
			continue
		}
		e := s.get(name)
		if r.fail {
			e.mayFail = true
			continue
		}
		if r.run.v87 {
			continue // no VHDL-1987 mode: nothing GoHard can check
		}
		e.runs = append(e.runs, run{
			opts:    Options{Standard: r.run.std, PSLComments: r.run.psl},
			why:     r.why,
			relaxed: r.run.relaxed,
		})
	}
	for _, r := range nvc {
		name := path.Join(s.dir, r.file)
		if !fileExists(name) {
			s.dangling = append(s.dangling, name)
			continue
		}
		e := s.get(name)
		if r.fail {
			e.mayFail = true
			continue
		}
		e.runs = append(e.runs, run{
			opts: Options{Standard: r.std, PSLComments: r.psl,
				Defines: r.defines},
			why: r.why,
		})
	}
	return s, nil
}

// ghdlMustParseRefs reads every testsuite.sh below root not in read
// (scripts outside expected_failures name no failures that count, but
// their analyze/synth calls must succeed) and the VESTS driver.
func ghdlMustParseRefs(root string, read map[string]bool) ([]ghdlRef,
	error,
) {
	var scripts []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "testsuite.sh" {
			return nil
		}
		r, _ := filepath.Rel(root, p)
		r = filepath.ToSlash(r)
		// The drivers (testsuite.sh, gna/testsuite.sh, ...) run the
		// per-test scripts; vests has its own driver.
		if strings.Count(r, "/") < 2 || strings.HasPrefix(r, "vests/") ||
			read[r] {
			return nil
		}
		scripts = append(scripts, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(scripts)
	var refs []ghdlRef
	for _, s := range scripts {
		got, err := readGHDLScript(root, s)
		if err != nil {
			return nil, err
		}
		for _, r := range got {
			// Outside gna/ and sanity/ an analyze_failure is not
			// part of the documented list; such a file is left
			// without expectation rather than gated.
			if !r.fail {
				refs = append(refs, r)
			}
		}
	}
	v, err := readVESTS(root)
	if err != nil {
		return nil, err
	}
	return append(refs, v...), nil
}

// sample returns up to 3 distinct sorted names.
func sample(names []string) []string {
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	return names[:min(3, len(names))]
}

func fileExists(repoRel string) bool {
	fi, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(repoRel)))
	return err == nil && fi.Mode().IsRegular()
}

// countVHDL counts the .vhd/.vhdl files below dir.
func countVHDL(dir string) (int, error) {
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && isVHDLName(p) {
			n++
		}
		return nil
	})
	if n == 0 && err == nil {
		err = errors.New("empty")
	}
	return n, err
}

// vhdlFiles lists the regular .vhd/.vhdl files below the repo-relative
// dir as sorted repo-relative slash paths.
func vhdlFiles(dir string) ([]string, error) {
	var out []string
	base := filepath.Join(repoRoot, filepath.FromSlash(dir))
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && isVHDLName(p) {
			r, _ := filepath.Rel(base, p)
			out = append(out, path.Join(dir, filepath.ToSlash(r)))
		}
		return nil
	})
	slices.Sort(out)
	return out, err
}

func readFile(t testing.TB, name string) []byte {
	t.Helper()
	src, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// regTally counts one source's outcome.
type regTally struct {
	files, mayFail, gated, gatedFail, ungated, ungatedErr int
}

// regFailure is one gated file that does not parse cleanly.
type regFailure struct {
	file, why, first string
	errors           int
}

func (f regFailure) String() string {
	return fmt.Sprintf("%s [%s]: %d syntax errors, first %s", f.file,
		f.why, f.errors, f.first)
}

// syntaxErrors parses src under o and returns the error diagnostics
// (inactive branches give hints, which do not count).
func syntaxErrors(t *testing.T, file string, src []byte,
	o Options,
) []diag.Diagnostic {
	t.Helper()
	r := Parse(src, o)
	if r.Root.String() != string(src) {
		t.Errorf("%s: round trip failed (%v)", file, o.Standard)
	}
	var out []diag.Diagnostic
	for _, d := range r.Diagnostics(file) {
		if d.Severity == diag.Error {
			out = append(out, d)
		}
	}
	return out
}

var anyStds = []token.Standard{token.Std2008, token.Std2019,
	token.Std1993}

// cleanAnyStd reports whether src parses without errors under one of
// anyStds, and otherwise the errors under the first.
func cleanAnyStd(t *testing.T, file string, src []byte) []diag.Diagnostic {
	var first []diag.Diagnostic
	for i, std := range anyStds {
		errs := syntaxErrors(t, file, src, Options{Standard: std})
		if len(errs) == 0 {
			return nil
		}
		if i == 0 {
			first = errs
		}
	}
	return first
}

func firstText(ds []diag.Diagnostic) string {
	d := ds[0]
	return fmt.Sprintf("%d:%d %s %s", d.Line, d.Col, d.Code, d.Message)
}

// checkSource parses every file of s and returns its tally and the
// gated failures. Round-trip failures are test errors in every mode.
func checkSource(t *testing.T, s *regSource) (regTally, []regFailure) {
	t.Helper()
	files, err := vhdlFiles(s.dir)
	if err != nil || len(files) == 0 {
		if s.submodule == "" {
			t.Fatalf("%s: %v (%d files)", s.dir, err, len(files))
		}
		t.Fatal(missingSubmodule(s.dir, s.submodule, err))
	}
	var (
		tl    regTally
		fails []regFailure
	)
	for _, f := range files {
		src := readFile(t, filepath.Join(repoRoot, filepath.FromSlash(f)))
		tl.files++
		e := s.exp[f]
		if e == nil {
			e = s.unlisted
		}
		switch {
		case e != nil && e.mayFail:
			tl.mayFail++
			syntaxErrors(t, f, src, Options{})
		case e != nil && len(e.runs) > 0:
			tl.gated++
			failed := false
			for _, r := range e.runs {
				errs := syntaxErrors(t, f, src, r.opts)
				if r.relaxed {
					errs = slices.DeleteFunc(errs,
						func(d diag.Diagnostic) bool {
							return d.Code == diag.SyntaxStandard
						})
				}
				if len(errs) > 0 {
					failed = true
					fails = append(fails, regFailure{file: f,
						why: fmt.Sprintf("%v, %s", r.opts.Standard,
							r.why),
						first: firstText(errs), errors: len(errs)})
					break // one line per file
				}
			}
			if failed {
				tl.gatedFail++
			}
		case e != nil && e.anyStd:
			tl.gated++
			if errs := cleanAnyStd(t, f, src); len(errs) > 0 {
				tl.gatedFail++
				fails = append(fails, regFailure{file: f,
					why:   "no recorded standard; tried 2008, 2019, 1993",
					first: firstText(errs), errors: len(errs)})
			}
		default:
			tl.ungated++
			if errs := cleanAnyStd(t, f, src); len(errs) > 0 {
				tl.ungatedErr++
			}
		}
	}
	return tl, fails
}

// TestParserRegression is the parser-regression runner (#31, #30): it
// round-trips every .vhd/.vhdl file of the bundled std/ieee packages
// (internal/stdlib, each under 2008 and 2019), the 14 corpus designs
// and the regression entries tagged pr in corpus.yaml (with
// -regression=nightly, also those tagged nightly), and requires zero
// syntax errors for every file the upstream project expects to
// analyse (corpus.yaml "Expected-failure extraction"): files on the
// GHDL/NVC expected-failure lists must only round-trip and not crash.
// A missing submodule fails with the git submodule update --init hint;
// nothing is skipped.
//
// Without -regression (plain go test) the gate only logs its failure
// count, so the package stays green while the grammar is incomplete;
// scripts/ci/regression.sh runs the gate.
func TestParserRegression(t *testing.T) {
	set := *regressionSet
	switch set {
	case "":
		if testing.Short() {
			t.Skip("-short; scripts/ci/regression.sh runs the gate")
		}
	case "pr", "nightly":
	default:
		t.Fatalf("-regression=%q: want pr or nightly", set)
	}
	sources := regressionSources(t, cmp.Or(set, "pr"))
	var all []regFailure
	for _, s := range sources {
		tl, fails := checkSource(t, s)
		t.Logf("regression %s (%s): %d files, %d gated, %d gated failing, "+
			"%d may fail, %d without expectation (%d with errors), %d "+
			"references to missing (generated) files %v", s.name, s.dir,
			tl.files, tl.gated, tl.gatedFail, tl.mayFail, tl.ungated,
			tl.ungatedErr, len(s.dangling), sample(s.dangling))
		all = append(all, fails...)
	}
	slices.SortFunc(all, func(a, b regFailure) int {
		return cmp.Or(cmp.Compare(a.file, b.file),
			cmp.Compare(a.why, b.why))
	})
	if len(all) == 0 {
		t.Logf("regression: every gated file parses without syntax errors")
		return
	}
	var b strings.Builder
	for _, f := range all {
		b.WriteString(f.String())
		b.WriteByte('\n')
	}
	if set == "" {
		t.Logf("regression: %d gated files have syntax errors (not "+
			"gated without -regression); first:\n%s", len(all),
			strings.Join(strings.SplitN(b.String(), "\n", 21)[:min(20,
				len(all))], "\n"))
		return
	}
	t.Errorf("regression: %d gated files have syntax errors:\n%s",
		len(all), b.String())
}
