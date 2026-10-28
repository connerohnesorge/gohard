package coverage_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/corpus"
	"github.com/connerohnesorge/gohard/internal/coverage"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
	"github.com/connerohnesorge/gohard/internal/simrun"
)

// TestGenhtml: genhtml (lcov 2) reads the coverage GoHard writes (#62)
// without a warning or error, branches included: every corpus golden
// (golden/coverage.info, run from the design's directory, where its
// SF paths resolve), the `gohard check --coverage` fixture's merged
// file, and the coverage of testdata/forms.vhd. It skips when genhtml
// is not on PATH, unless GOHARD_REQUIRE_GENHTML=1 (the genhtml CI job,
// scripts/ci/genhtml.sh; nix develop provides lcov).
func TestGenhtml(t *testing.T) {
	e := realenv.New(realenv.Options{Dir: t.TempDir()})
	bin, err := e.Executor.LookPath("genhtml")
	if err != nil {
		if os.Getenv("GOHARD_REQUIRE_GENHTML") == "1" {
			t.Fatalf("genhtml is not on PATH: %v", err)
		}
		t.Skip("genhtml is not on PATH; run in nix develop")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	type input struct{ name, dir, file string }
	var ins []input
	golden, err := filepath.Glob(filepath.Join(root, "testdata", "corpus",
		"*", "golden", "coverage.info"))
	if err != nil || len(golden) == 0 {
		t.Fatalf("no corpus coverage goldens: %v", err)
	}
	for _, g := range golden {
		dir := filepath.Dir(filepath.Dir(g))
		ins = append(ins, input{filepath.Base(dir), dir,
			filepath.Join("golden", "coverage.info")})
	}
	fx := filepath.Join(root, "testdata", "cli", "check-sim-coverage",
		"want")
	ins = append(ins, input{"check-sim-coverage", fx,
		filepath.FromSlash(coverage.Merged)})
	r, src := formsRun(t)
	res, err := corpus.Execute(r, src, corpus.Options{
		Code: simrun.DefaultCode, Coverage: true})
	if err != nil || res.Coverage == nil {
		t.Fatalf("forms: %v", err)
	}
	forms := filepath.Join(t.TempDir(), "forms.info")
	if err := os.WriteFile(forms, coverage.Encode(*res.Coverage),
		0o644); err != nil {
		t.Fatal(err)
	}
	ins = append(ins, input{"forms", filepath.Join(root, "internal",
		"coverage", "testdata"), forms})
	out := t.TempDir()
	for _, in := range ins {
		res, err := e.Executor.Run(context.Background(), env.Command{
			Path: bin, Dir: in.dir,
			Args: []string{"--branch-coverage", "-o",
				filepath.Join(out, in.name), in.file}})
		if err != nil {
			t.Fatal(err)
		}
		msg := string(res.Stdout) + string(res.Stderr)
		if res.ExitCode != 0 || strings.Contains(msg, "WARNING:") ||
			strings.Contains(msg, "ERROR:") ||
			!strings.Contains(msg, "no messages were reported") {
			t.Errorf("%s: genhtml exit %d:\n%s", in.name, res.ExitCode,
				msg)
			continue
		}
		if _, err := os.Stat(filepath.Join(out, in.name,
			"index.html")); err != nil {
			t.Errorf("%s: %v", in.name, err)
		}
	}
}
