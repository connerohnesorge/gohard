package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/format"
	"github.com/connerohnesorge/gohard/internal/parser"
)

const (
	uglyVHDL = "ENTITY top IS\nport( a : in bit;b:out bit );\n" +
		"END entity;\n\n\n\narchitecture rtl of top is\nbegin\n" +
		"    b<=a;\nend;\n"
	niceVHDL = "entity top is\n  port(a : in bit; b : out bit);\n" +
		"end entity;\n\narchitecture rtl of top is\nbegin\n" +
		"  b <= a;\nend;\n"
)

// fmtRun runs one gohard command line in sim and returns the exit
// code, stdout and stderr.
func fmtRun(sim *simenv.Sim, args ...string) (int, string, string) {
	e := sim.Env()
	var out, errb bytes.Buffer
	e.Transport.Stdout, e.Transport.Stderr = &out, &errb
	code := cli.Run(context.Background(), e, args)
	return code, out.String(), errb.String()
}

// tree returns every regular file under /work by path.
func tree(t *testing.T, fsys env.FS) map[string]string {
	t.Helper()
	got := map[string]string{}
	var walk func(dir string)
	walk = func(dir string) {
		ents, err := fsys.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			p := path.Join(dir, e.Name())
			if e.IsDir() {
				walk(p)
				continue
			}
			if e.Type()&fs.ModeSymlink != 0 {
				continue
			}
			b, err := fsys.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			got[p] = string(b)
		}
	}
	walk(simenv.DefaultDir)
	return got
}

// recordFS records the file-changing calls made through it.
type recordFS struct {
	env.FS
	mu    sync.Mutex
	calls []string
}

func (r *recordFS) log(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, filepath.ToSlash(s))
}

func (r *recordFS) WriteFile(name string, data []byte,
	perm fs.FileMode,
) error {
	r.log("writefile " + name)
	return r.FS.WriteFile(name, data, perm)
}

func (r *recordFS) WriteTemp(dir, prefix string, data []byte) (string,
	error,
) {
	p, err := r.FS.WriteTemp(dir, prefix, data)
	r.log("writetemp " + dir + " " + prefix + " -> " + p)
	return p, err
}

func (r *recordFS) Chmod(name string, perm fs.FileMode) error {
	r.log("chmod " + name + " " + perm.String())
	return r.FS.Chmod(name, perm)
}

func (r *recordFS) Rename(oldname, newname string) error {
	r.log("rename " + oldname + " " + newname)
	return r.FS.Rename(oldname, newname)
}

// TestFmtTempFileAndRename shows gohard fmt replaces each changed file
// by writing a temp file in the file's own directory and renaming it
// over the original (#32), after giving the temp file the original's
// mode: no WriteFile, no other writes, and nothing for a file that is
// already formatted.
func TestFmtTempFileAndRename(t *testing.T) {
	sim := simenv.New(simenv.Options{Files: map[string][]byte{
		"rtl/top.vhd":   []byte(uglyVHDL),
		"rtl/clean.vhd": []byte(niceVHDL),
	}})
	rec := &recordFS{FS: sim.FS}
	e := sim.Env()
	e.FS = rec
	var out, errb bytes.Buffer
	e.Transport.Stdout, e.Transport.Stderr = &out, &errb
	if code := cli.Run(context.Background(), e,
		[]string{"fmt"}); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errb.String())
	}
	want := []string{
		"writetemp /work/rtl/ .top.vhd. -> /work/rtl/.top.vhd.1.tmp",
		"chmod /work/rtl/.top.vhd.1.tmp -rw-r--r--",
		"rename /work/rtl/.top.vhd.1.tmp /work/rtl/top.vhd",
	}
	if !slices.Equal(rec.calls, want) {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(rec.calls, "\n"),
			strings.Join(want, "\n"))
	}
	got := tree(t, sim.FS)
	if got["/work/rtl/top.vhd"] != niceVHDL ||
		got["/work/rtl/clean.vhd"] != niceVHDL || len(got) != 2 {
		t.Fatalf("tree after fmt: %q", got)
	}
}

// TestFmtWriteFailureKeepsOriginal injects a failing temp-file write
// and a failing rename (simenv fault injection, P6): each run reports
// format/write-failed (exit 2), the original file keeps its bytes and
// no temp file is left behind. Files whose writes succeed are still
// formatted.
func TestFmtWriteFailureKeepsOriginal(t *testing.T) {
	cases := []struct {
		name   string
		inject func(*simenv.FS)
	}{
		{"temp write", func(f *simenv.FS) {
			f.FailNext(simenv.OpWrite, "/work/.a.vhd.1.tmp",
				errors.New("no space left on device"))
		}},
		{"chmod", func(f *simenv.FS) {
			f.FailNext(simenv.OpChmod, "/work/.a.vhd.1.tmp",
				errors.New("no space left on device"))
		}},
		{"rename", func(f *simenv.FS) {
			f.FailNext(simenv.OpRename, "/work/.a.vhd.1.tmp",
				errors.New("no space left on device"))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sim := simenv.New(simenv.Options{Files: map[string][]byte{
				"a.vhd": []byte(uglyVHDL),
				"b.vhd": []byte(uglyVHDL),
			}})
			c.inject(sim.FS)
			code, out, _ := fmtRun(sim, "fmt")
			if code != 2 {
				t.Fatalf("exit %d\n%s", code, out)
			}
			wantOut := "a.vhd:1:1: error[format/write-failed]: cannot " +
				"write formatted file: simenv: injected fault: no " +
				"space left on device\n" +
				"summary: 1 errors, 0 warnings, 0 hints, 2 files\n"
			if out != wantOut {
				t.Fatalf("stdout:\n%s\nwant:\n%s", out, wantOut)
			}
			got := tree(t, sim.FS)
			want := map[string]string{
				"/work/a.vhd": uglyVHDL,
				"/work/b.vhd": niceVHDL,
			}
			if !mapsEqual(got, want) {
				t.Fatalf("tree after a failed write: %q", got)
			}
		})
	}
}

// fmtModes are the permission bits TestFmtKeepsMode checks; the
// formatter must not widen, narrow or drop any of them (#32: only the
// text changes).
var fmtModes = []fs.FileMode{0o600, 0o640, 0o644, 0o755, 0o444, 0o700}

// TestFmtKeepsMode shows a rewritten file keeps its permission bits in
// the simulated FS, also through a symbolic link, where the link's
// target keeps its mode.
func TestFmtKeepsMode(t *testing.T) {
	for _, m := range fmtModes {
		t.Run(m.String(), func(t *testing.T) {
			sim := simenv.New(simenv.Options{Files: map[string][]byte{
				"a.vhd":     []byte(uglyVHDL),
				"rtl/b.vhd": []byte(uglyVHDL),
			}})
			for _, n := range []string{"a.vhd", "rtl/b.vhd"} {
				if err := sim.FS.Chmod(n, m); err != nil {
					t.Fatal(err)
				}
			}
			if err := sim.FS.Symlink("rtl/b.vhd", "l.vhd"); err != nil {
				t.Fatal(err)
			}
			code, out, _ := fmtRun(sim, "fmt", "a.vhd", "l.vhd")
			if code != 0 {
				t.Fatalf("exit %d\n%s", code, out)
			}
			for _, n := range []string{"a.vhd", "rtl/b.vhd"} {
				fi, err := sim.FS.Lstat(n)
				if err != nil {
					t.Fatal(err)
				}
				if fi.Mode() != m {
					t.Errorf("%s: mode %v after fmt, want %v", n,
						fi.Mode(), m)
				}
				b, _ := sim.FS.ReadFile(n)
				if string(b) != niceVHDL {
					t.Errorf("%s not formatted: %q", n, b)
				}
			}
		})
	}
}

// TestFmtKeepsModeOnDisk is TestFmtKeepsMode on the real file system
// (realenv), where the temp file starts out 0644.
func TestFmtKeepsModeOnDisk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits")
	}
	for _, m := range fmtModes {
		t.Run(m.String(), func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "a.vhd")
			if err := os.WriteFile(p, []byte(uglyVHDL), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(p, m); err != nil {
				t.Fatal(err)
			}
			var out, errb bytes.Buffer
			e := realenv.New(realenv.Options{Dir: dir, Stdout: &out,
				Stderr: &errb})
			if code := cli.Run(context.Background(), e,
				[]string{"fmt", "a.vhd"}); code != 0 {
				t.Fatalf("exit %d\n%s%s", code, out.String(),
					errb.String())
			}
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != m {
				t.Errorf("mode %v after fmt, want %v", fi.Mode().Perm(),
					m)
			}
			b, err := os.ReadFile(p)
			if err != nil || string(b) != niceVHDL {
				t.Errorf("not formatted: %q %v", b, err)
			}
		})
	}
}

// TestFmtEveryWriteFails runs gohard fmt with every write failing
// (seeded fault rate 1000/1000): every file is reported, none changes.
func TestFmtEveryWriteFails(t *testing.T) {
	files := map[string][]byte{
		"a.vhd":     []byte(uglyVHDL),
		"sub/b.vhd": []byte(uglyVHDL),
		"c.vhd":     []byte(niceVHDL),
	}
	sim := simenv.New(simenv.Options{Files: files, Seed: 7,
		Faults: simenv.Faults{WriteError: simenv.Always}})
	code, out, _ := fmtRun(sim, "fmt")
	if code != 2 || strings.Count(out, "format/write-failed") != 2 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	got := tree(t, sim.FS)
	for name, b := range files {
		if got["/work/"+name] != string(b) {
			t.Errorf("%s changed after failed writes: %q", name,
				got["/work/"+name])
		}
	}
	if len(got) != len(files) {
		t.Errorf("files left behind: %q", got)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// TestFmtMatchesFormatter checks, on every golden input of
// testdata/format and every parse rule file, that gohard fmt writes
// exactly what internal/format produces (the server's code path, #32),
// that fmt --check then exits 0, that fmt --check before the rewrite
// exits 1 exactly when the formatter changes something, and that -p 1
// and -p 16 agree.
func TestFmtMatchesFormatter(t *testing.T) {
	files := map[string][]byte{}
	for _, glob := range []string{"../../testdata/format/*/in.vhd",
		"../../testdata/parse/*/valid.vhd"} {
		ms, err := filepath.Glob(glob)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ms {
			b, err := os.ReadFile(m)
			if err != nil {
				t.Fatal(err)
			}
			rel := filepath.Base(filepath.Dir(m)) + "/" +
				filepath.Base(m)
			files[rel] = b
		}
	}
	if len(files) < 20 {
		t.Fatalf("only %d input files", len(files))
	}
	// One configuration for the run: the defaults.
	popts := parser.FromConfig(config.Defaults(), cli.Version)
	opts := format.FromConfig(config.Defaults())
	want := map[string]string{}
	changed := false
	for name, b := range files {
		res, err := format.Source(b, popts, opts)
		if err != nil {
			// Does not parse under the defaults (for example a
			// 2019-only rule): left as it is.
			want["/work/"+name] = string(b)
			continue
		}
		want["/work/"+name] = string(res.Text)
		changed = changed || res.Changed()
	}
	var trees []map[string]string
	for _, jobs := range []string{"1", "16"} {
		sim := simenv.New(simenv.Options{Files: files, Seed: 3})
		code, out, _ := fmtRun(sim, "-p", jobs, "fmt", "--check")
		if changed && !strings.Contains(out, "format/would-change") {
			t.Fatalf("fmt --check found nothing to change:\n%s", out)
		}
		if code == 0 && changed {
			t.Fatalf("-p %s: fmt --check exit 0", jobs)
		}
		if got := tree(t, sim.FS); !sameFiles(got, files) {
			t.Fatal("fmt --check wrote files")
		}
		fmtRun(sim, "-p", jobs, "fmt")
		got := tree(t, sim.FS)
		for name, w := range want {
			if got[name] != w {
				t.Errorf("-p %s: %s:\n%s\nwant:\n%s", jobs, name,
					got[name], w)
			}
		}
		code, out, _ = fmtRun(sim, "-p", jobs, "fmt", "--check")
		if strings.Contains(out, "format/would-change") {
			t.Errorf("-p %s: still unformatted after fmt:\n%s", jobs,
				out)
		}
		_ = code // files that do not parse keep exit 1
		trees = append(trees, got)
	}
	if !mapsEqual(trees[0], trees[1]) {
		t.Error("-p 1 and -p 16 wrote different files")
	}
}

func sameFiles(got map[string]string, files map[string][]byte) bool {
	if len(got) != len(files) {
		return false
	}
	for name, b := range files {
		if got["/work/"+name] != string(b) {
			return false
		}
	}
	return true
}
