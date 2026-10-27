package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
)

// TestImportThenCheck is the #2 acceptance check: for a ModelSim .do
// script, a Vivado .xpr project and a Quartus .qsf file, gohard import
// writes a gohard.yaml with which gohard check passes. The design needs
// the mapping: top.vhd uses library mylib, which only the imported
// libraries: entry provides.
func TestImportThenCheck(t *testing.T) {
	for _, tc := range []struct{ fixture, file string }{
		{"import-do", "sim/compile.do"},
		{"import-xpr", "proj/counter.xpr"},
		{"import-qsf", "counter.qsf"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, readTree(t, filepath.Join(fixtureRoot,
				tc.fixture, "in")))
			run := func(args ...string) (int, string) {
				var stdout, stderr bytes.Buffer
				e := realenv.New(realenv.Options{Stdout: &stdout,
					Stderr: &stderr, Dir: dir})
				code := cli.Run(context.Background(), e, args)
				return code, stdout.String() + stderr.String()
			}
			if code, out := run("import", tc.file); code != 0 {
				t.Fatalf("gohard import: exit %d\n%s", code, out)
			}
			code, out := run("check")
			if code != 0 || !strings.Contains(out,
				"summary: 0 errors, 0 warnings, 0 hints, 3 files") {
				t.Errorf("gohard check: exit %d\n%s", code, out)
			}
			// Without the imported mapping the same project fails.
			write(t, filepath.Join(dir, "gohard.yaml"), nil)
			if code, out := run("check"); code != 1 ||
				!strings.Contains(out, "library/unmapped") {
				t.Errorf("gohard check without the mapping: exit %d\n%s",
					code, out)
			}
		})
	}
}
