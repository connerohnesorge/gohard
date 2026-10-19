package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cli"
)

const lspRoot = "../../testdata/lsp"

// TestLSPActionFixtures runs every testdata/lsp/<action>/<case> (#26):
// a `gohard lsp <action> --at PATH:LINE:COL` run in the testdata/cli
// fixture format (cmd, in/, want.exit, want.stdout, want.stderr), with
// the same harness as TestCLIFixtures. session/ and record/ hold the
// session fixtures of the server and are not actions.
func TestLSPActionFixtures(t *testing.T) {
	actions, err := os.ReadDir(lspRoot)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range actions {
		if !a.IsDir() || a.Name() == "session" || a.Name() == "record" {
			continue
		}
		cases, err := os.ReadDir(filepath.Join(lspRoot, a.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			if !c.IsDir() {
				continue
			}
			n++
			dir := filepath.Join(lspRoot, a.Name(), c.Name())
			t.Run(a.Name()+"/"+c.Name(), func(t *testing.T) {
				t.Parallel()
				runFixture(t, dir, cli.Run)
			})
		}
	}
	if n == 0 {
		t.Fatal("no action fixtures under " + lspRoot)
	}
}
