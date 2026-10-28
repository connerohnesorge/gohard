package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// countFS cancels the run at the first VHDL file read and counts every
// call that reaches the disk afterwards. It is not safe for parallel
// use, so the test runs at -p 1.
type countFS struct {
	env.FS
	cancel func()
	after  *int
	fired  *bool
}

func (c countFS) note() {
	if *c.fired {
		*c.after++
	}
}

func (c countFS) ReadFile(name string) ([]byte, error) {
	c.note()
	if !*c.fired && strings.HasSuffix(name, ".vhd") {
		*c.fired = true
		c.cancel()
	}
	return c.FS.ReadFile(name)
}

func (c countFS) ReadDir(name string) ([]fs.DirEntry, error) {
	c.note()
	return c.FS.ReadDir(name)
}

func (c countFS) Stat(name string) (fs.FileInfo, error) {
	c.note()
	return c.FS.Stat(name)
}

func (c countFS) Lstat(name string) (fs.FileInfo, error) {
	c.note()
	return c.FS.Lstat(name)
}

// TestInterruptStopsWork: once SIGINT or SIGTERM cancels the run, no
// further file access reaches the disk, so a long check ends at once
// with exit 130/143, JSON status `interrupted`, and cli/interrupted as
// its only diagnostic (the abandoned work's partial findings are not
// reported).
func TestInterruptStopsWork(t *testing.T) {
	for _, intr := range []env.Interrupt{env.SIGINT, env.SIGTERM} {
		for _, p := range []string{"1"} {
			e, out, _ := newEnv(t)
			dir, _ := e.FS.Getwd()
			if err := os.WriteFile(filepath.Join(dir, "gohard.yaml"),
				[]byte("lenient: true\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			for i := range 200 {
				d := filepath.Join(dir, fmt.Sprintf("d%03d", i))
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(d, "a.vhd"),
					[]byte("entity a is end;\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			var after int
			var fired bool
			e.FS = countFS{FS: e.FS, cancel: func() { cancel(intr) },
				after: &after, fired: &fired}
			code := Run(ctx, e, []string{"-p", p, "check", "--format",
				"json"})
			if code != intr.Exit {
				t.Fatalf("%s -p %s: exit %d", intr.Signal, p, code)
			}
			if after != 0 {
				t.Errorf("%s -p %s: %d file accesses after the signal",
					intr.Signal, p, after)
			}
			var rep struct {
				Status      string
				Diagnostics []struct{ Code string }
			}
			if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
				t.Fatal(err)
			}
			if rep.Status != "interrupted" || len(rep.Diagnostics) != 1 ||
				rep.Diagnostics[0].Code != "cli/interrupted" {
				t.Errorf("%s -p %s: %s", intr.Signal, p, out.String())
			}
		}
	}
}
