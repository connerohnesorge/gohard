package cli_test

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/corpus"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/token"
)

// conformanceRoot is corpus.ConformanceDir from this package.
const conformanceRoot = "../../" + corpus.ConformanceDir

// conformanceCase is one case file found under conformanceRoot.
type conformanceCase struct {
	corpus.ConformanceCase
	dir string // the feature folder on disk
	src []byte
}

// loadConformance reads every testdata/conformance/<feature>/<case>.vhd
// and checks that every other file there belongs to a case.
func loadConformance(t *testing.T) []conformanceCase {
	t.Helper()
	feats, err := os.ReadDir(conformanceRoot)
	if err != nil {
		t.Fatal(err)
	}
	var cases []conformanceCase
	for _, f := range feats {
		if !f.IsDir() {
			continue
		}
		dir := filepath.Join(conformanceRoot, f.Name())
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range ents {
			if n, ok := strings.CutSuffix(e.Name(), ".vhd"); ok {
				names = append(names, n)
			}
		}
		for _, e := range ents {
			base := e.Name()
			for _, ext := range []string{".vhd", ".want", ".nvc.txt"} {
				base = strings.TrimSuffix(base, ext)
			}
			if e.IsDir() || !slices.Contains(names, base) {
				t.Errorf("%s/%s: not a case file (want <case>.vhd, "+
					"<case>.want or <case>.nvc.txt)", f.Name(), e.Name())
			}
		}
		for _, n := range names {
			src := read(t, filepath.Join(dir, n+".vhd"))
			c, err := corpus.ParseConformanceCase(f.Name(), n, src)
			if err != nil {
				t.Fatal(err)
			}
			cases = append(cases, conformanceCase{c, dir, src})
		}
	}
	if len(cases) == 0 {
		t.Fatal("no conformance cases under " + conformanceRoot)
	}
	return cases
}

// TestConformance runs every #18 conformance case (format in
// testdata/conformance/README.md): the parsed CST prints back to the
// file byte for byte, and gohard check --sim --seed 0 gives exactly the
// .want output at -p 1 and -p 16. -update rewrites the .want files.
func TestConformance(t *testing.T) {
	for _, c := range loadConformance(t) {
		t.Run(c.Feature+"/"+c.Name, func(t *testing.T) {
			t.Parallel()
			runConformance(t, c)
		})
	}
}

func runConformance(t *testing.T, c conformanceCase) {
	std, ok := token.ParseStandard(c.Std)
	if !ok {
		t.Fatalf("std %q", c.Std)
	}
	res := parser.Parse(c.src, parser.Options{Standard: std})
	if got := res.Root.AppendText(nil); !bytes.Equal(got, c.src) {
		t.Fatalf("CST round trip differs from %s:\n%s", c.File(), got)
	}
	if len(res.Problems) > 0 && c.Pending == "" {
		t.Errorf("syntax problems: %v", res.Diagnostics(c.File()))
	}
	// A fixture dir with in/ only, run like testdata/cli cases.
	fix := t.TempDir()
	in := map[string][]byte{c.File(): c.src}
	if cfg := c.Config(); cfg != nil {
		in["gohard.yaml"] = cfg
	}
	writeTree(t, filepath.Join(fix, "in"), in)
	var first outcome
	for i, p := range jobCounts {
		args := append([]string{"-p", strconv.Itoa(p)}, c.CheckArgs()...)
		got := runOnce(t, fix, args, nil, cli.Run, nil)
		if i == 0 {
			first = got
		} else if !sameOutcome(got, first, false) {
			t.Fatalf("-p %d differs from -p %d:\n%s\nvs\n%s", p,
				jobCounts[0], got.stdout, first.stdout)
		}
	}
	if skip, err := pendingSkip(c.ConformanceCase,
		first.stdout); err != nil {
		t.Fatal(err)
	} else if skip {
		t.Skipf("pending: %s is still unsupported/construct", c.Pending)
	}
	got := c.RenderWant(first.exit, first.stdout, first.stderr)
	wantPath := filepath.Join(c.dir, c.WantFile())
	if *update {
		write(t, wantPath, got)
		return
	}
	compare(t, path.Join(c.Feature, c.WantFile()), got,
		read(t, wantPath))
}

// pendingSkip reports whether a case is skipped: it has a pending:
// header and its run reports that construct as unsupported/construct.
// A pending case reporting some other unsupported construct is an
// error (the header names the wrong construct); a pending case that
// runs is compared like any other, so builders can record its .want
// before the integrator removes the header.
func pendingSkip(c corpus.ConformanceCase, stdout []byte) (bool, error) {
	if c.Pending == "" {
		return false, nil
	}
	const code = "[unsupported/construct]: "
	if bytes.Contains(stdout, []byte(code+c.Pending+
		" is not supported")) {
		return true, nil
	}
	if bytes.Contains(stdout, []byte(code)) {
		return false, &pendingError{c.Pending, stdout}
	}
	return false, nil
}

type pendingError struct {
	construct string
	stdout    []byte
}

func (e *pendingError) Error() string {
	return "pending: " + e.construct + " does not match the " +
		"unsupported/construct message:\n" + string(e.stdout)
}

// TestPendingSkip: only the named construct skips a pending case; a
// case without the header never skips; a wrong name is an error.
func TestPendingSkip(t *testing.T) {
	out := []byte("a.vhd:8:5: error[unsupported/construct]: " +
		"conditional return ... when is not supported yet " +
		"(planned for v1.1)\n")
	pend := corpus.ConformanceCase{Pending: "conditional return ... when"}
	if skip, err := pendingSkip(pend, out); !skip || err != nil {
		t.Errorf("matching construct: skip %v, %v", skip, err)
	}
	if skip, err := pendingSkip(corpus.ConformanceCase{},
		out); skip || err != nil {
		t.Errorf("no header: skip %v, %v", skip, err)
	}
	wrong := corpus.ConformanceCase{Pending: "conditional return"}
	if _, err := pendingSkip(wrong, out); err == nil {
		t.Error("prefix of the construct name accepted")
	}
	if skip, err := pendingSkip(pend, []byte("summary: 0 errors\n")); skip ||
		err != nil {
		t.Errorf("supported now: skip %v, %v", skip, err)
	}
}
