package format

import (
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

	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/token"
)

var prRegression = flag.Bool("format.regression", false,
	"check the format invariants on every file of the PR regression "+
		"set (testdata/ghdl/testsuite, testdata/nvc/test; minutes)")

// regressionStds are tried in order; a file is checked under the first
// standard it parses with.
var regressionStds = []token.Standard{token.Std2008, token.Std2019,
	token.Std1993}

// prRegressionDirs returns the repo-relative folders of the regression
// entries tagged pr in testdata/corpus/corpus.yaml (the GHDL and NVC
// test folders), sorted.
func prRegressionDirs(t *testing.T) []string {
	t.Helper()
	src := mustRead(t, "../../testdata/corpus/corpus.yaml")
	var m struct {
		Regression []struct {
			Name, Path, Tag, Subfolder string
		}
	}
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("corpus.yaml: %v", err)
	}
	var dirs []string
	for _, e := range m.Regression {
		if e.Tag == "pr" {
			dirs = append(dirs, path.Join(e.Path, path.Clean(e.Subfolder)))
		}
	}
	if len(dirs) == 0 {
		t.Fatal("corpus.yaml: no regression entry tagged pr")
	}
	slices.Sort(dirs)
	return dirs
}

// TestRegressionSetInvariants checks the three #32 invariants
// (checkInvariants) on every .vhd/.vhdl file of the PR regression set.
// Each file is checked under the first standard of 2008, 2019 and
// 1993 it parses with, with the default options and with one more
// option set chosen by the file's hash (fuzzConfigs). Files that parse
// under none (the upstream expected failures) are counted, not
// checked. A missing submodule checkout fails. The set takes minutes,
// so it runs with -format.regression (scripts/ci/regression.sh pr).
func TestRegressionSetInvariants(t *testing.T) {
	if !*prRegression {
		t.Skip("run with -format.regression")
	}
	for _, dir := range prRegressionDirs(t) {
		t.Run(path.Base(path.Dir(dir)), func(t *testing.T) {
			checkRegressionDir(t, dir)
		})
	}
}

func checkRegressionDir(t *testing.T, dir string) {
	root := filepath.Join("..", "..", filepath.FromSlash(dir))
	var files []string
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry,
		err error) error {
		if err == nil && !e.IsDir() && (strings.HasSuffix(p, ".vhd") ||
			strings.HasSuffix(p, ".vhdl")) {
			files = append(files, p)
		}
		return err
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("%s: no VHDL files (%v); run git submodule update "+
			"--init %s", dir, err, path.Dir(dir))
	}
	slices.Sort(files)
	checked, skipped, bad := 0, 0, 0
	for _, p := range files {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		err = checkRegressionFile(src)
		switch {
		case errors.Is(err, errNoStandard):
			skipped++
		case err != nil:
			bad++
			if bad <= 10 {
				t.Errorf("%s: %s", filepath.ToSlash(p), trunc(err.Error()))
			}
			checked++
		default:
			checked++
		}
	}
	t.Logf("%s: %d files, %d checked, %d do not parse", dir, len(files),
		checked, skipped)
	if bad > 0 {
		t.Errorf("%s: %d of %d checked files break a format invariant",
			dir, bad, checked)
	}
	if checked == 0 {
		t.Errorf("%s: no file parses", dir)
	}
}

var errNoStandard = errors.New("parses under no standard")

// checkRegressionFile checks one file under the first standard it
// parses with.
func checkRegressionFile(src []byte) error {
	for _, std := range regressionStds {
		popts := parser.Options{Standard: std}
		if !Parses(parser.Parse(src, popts)) {
			continue
		}
		def := Default()
		def.Standard = std
		other := fuzzConfigs(src)[1].opts
		other.Standard = std
		for _, o := range []Options{def, other} {
			if err := checkInvariants(src, popts, o); err != nil {
				return fmt.Errorf("std %v, %+v: %w", std, o, err)
			}
		}
		return nil
	}
	return errNoStandard
}
