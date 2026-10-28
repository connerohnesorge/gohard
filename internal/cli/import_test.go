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

// TestImportThenCheck is the #2 and #64 acceptance check: for a
// ModelSim .do script (one run from its own folder, one run from the
// project folder with vcom wildcards), a Vivado .xpr project, a Quartus
// .qsf file, an OSVVM .pro script and a FuseSoC .core file (with a
// depend: on another .core under the root), gohard import writes a
// gohard.yaml with which gohard check passes.
// The design needs the mapping: it uses library mylib, which only the
// imported libraries: entry provides.
func TestImportThenCheck(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		args    []string
	}{
		{"import-do", []string{"--dir", "sim", "sim/compile.do"}},
		{"import-do-glob", []string{"scripts/compile.do"}},
		{"import-xpr", []string{"proj/counter.xpr"}},
		{"import-qsf", []string{"counter.qsf"}},
		{"import-pro", []string{"build.pro"}},
		{"import-core", []string{"rtl/top.core"}},
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
			if code, out := run(append([]string{"import"},
				tc.args...)...); code != 0 {
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
