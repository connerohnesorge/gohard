package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
)

// TestCoverageMergeOrder: `gohard coverage merge` gives the same bytes
// for every order of its files (#62), and merging the per-testbench
// files of a `gohard check --coverage` run gives that run's lcov.info.
func TestCoverageMergeOrder(t *testing.T) {
	fx := filepath.Join("..", "..", "testdata", "cli",
		"check-sim-coverage", "want",
		".gohard", "coverage")
	dir := t.TempDir()
	names := []string{"tb_a-0.info", "tb_b-0.info", "lcov.info"}
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(fx, n))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	merge := func(files ...string) []byte {
		t.Helper()
		var out, errb bytes.Buffer
		e := realenv.New(realenv.Options{Dir: dir, Stdout: &out,
			Stderr: &errb})
		args := append([]string{"coverage", "merge"}, files...)
		if code := Run(context.Background(), e, args); code != 0 {
			t.Fatalf("%v: exit %d: %s%s", args, code, out.String(),
				errb.String())
		}
		return out.Bytes()
	}
	lcov, err := os.ReadFile(filepath.Join(fx, "lcov.info"))
	if err != nil {
		t.Fatal(err)
	}
	if got := merge("tb_b-0.info", "tb_a-0.info"); !bytes.Equal(got,
		lcov) {
		t.Fatalf("merge of the testbench files differs from lcov.info:"+
			"\n%s\n---\n%s", got, lcov)
	}
	var want []byte
	for _, p := range [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0},
		{2, 0, 1}, {2, 1, 0}} {
		got := merge(names[p[0]], names[p[1]], names[p[2]])
		if want == nil {
			want = got
		} else if !bytes.Equal(got, want) {
			t.Fatalf("order %v: merge differs:\n%s\n---\n%s", p, want,
				got)
		}
	}
}
