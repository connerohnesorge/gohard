package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/cli"
	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
	"github.com/connerohnesorge/gohard/v2/internal/workspace"
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

// TestImportTooLarge checks that gohard import reads no file of more
// than workspace.MaxFileSize bytes (P7): not the project file, not a
// script it includes and not a .core file found under the root. The
// large files are sparse, so the test writes almost nothing.
func TestImportTooLarge(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string][]byte{
		".git/HEAD": nil,
		"a.vhd":     []byte("entity a is end;\n"),
		"inc.pro":   []byte("include big.pro\nanalyze a.vhd\n"),
		"top.core": []byte("CAPI=2:\nfilesets: {r: {files: [a.vhd], " +
			"file_type: vhdlSource, depend: [x:y:big]}}\n"),
	})
	for _, name := range []string{"big.pro", "big.core", "dep/big.core"} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		// The core x:y:big, padded with zero bytes past the limit.
		write(t, p, []byte("CAPI=2:\nname: x:y:big:1\n"))
		if err := os.Truncate(p, workspace.MaxFileSize+1); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (int, string) {
		var stdout, stderr bytes.Buffer
		e := realenv.New(realenv.Options{Stdout: &stdout,
			Stderr: &stderr, Dir: dir})
		code := cli.Run(context.Background(), e, args)
		return code, stdout.String() + stderr.String()
	}
	tooLarge := "it is larger than 10000000 bytes"
	for _, tc := range []struct {
		file string
		code int
		want string
	}{
		{"big.core", 2, "error[workspace/unreadable]: cannot read " +
			"big.core: " + tooLarge},
		{"inc.pro", 2, "inc.pro:1:9: error[workspace/unreadable]: " +
			"cannot read big.pro: " + tooLarge},
		// dep/big.core and big.core provide no core.
		{"top.core", 0, "top.core:2:64: warning[import/" +
			"unresolved-dependency]: dependency x:y:big matches no"},
	} {
		code, out := run("import", tc.file)
		if code != tc.code || !strings.Contains(out, tc.want) {
			t.Errorf("gohard import %s: exit %d, want %d and %q\n%s",
				tc.file, code, tc.code, tc.want, out)
		}
	}
}
