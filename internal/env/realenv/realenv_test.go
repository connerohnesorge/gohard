package realenv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/env"
)

func TestPoolCanonicalOrder(t *testing.T) {
	in := make([]int, 1000)
	for i := range in {
		in[i] = i
	}
	want := env.Map(env.Sequential{}, 1, in, func(x int) int { return 3 * x })
	for _, limit := range []int{0, 1, 2, 16, 5000} {
		e := New(Options{Workers: 4})
		var calls atomic.Int64
		got := env.Map(e.Jobs, limit, in, func(x int) int {
			calls.Add(1)
			return 3 * x
		})
		if !slices.Equal(got, want) || calls.Load() != int64(len(in)) {
			t.Fatalf("limit %d: wrong results or %d calls",
				limit, calls.Load())
		}
	}
}

func TestPoolPanicReachesCaller(t *testing.T) {
	defer func() {
		if r := recover(); r != "boom" {
			t.Fatalf("recovered %v, want boom", r)
		}
	}()
	New(Options{}).Jobs.Run(8, 100, func(i int) {
		if i == 50 {
			panic("boom")
		}
	})
	t.Fatal("panic was swallowed")
}

// TestPoolPanicLowestIndex: whichever job panics first in time, Run
// re-raises the panic of the lowest panicking index, the one
// env.Sequential raises, so a crash report is the same at every -p.
func TestPoolPanicLowestIndex(t *testing.T) {
	catch := func(limit, n int, job func(int)) (r any) {
		defer func() { r = recover() }()
		New(Options{}).Jobs.Run(limit, n, job)
		return nil
	}
	for _, lowest := range []int{0, 3, 17} {
		// Job lowest spins before panicking, so at -p > 1 the jobs
		// above it panic first in wall-clock time.
		job := func(i int) {
			if i < lowest {
				return
			}
			if i == lowest {
				x := 0
				for k := range 20_000_000 {
					x ^= k
				}
				runtime.KeepAlive(x)
			}
			panic(i)
		}
		want := catch(1, 64, job)
		if want != lowest {
			t.Fatalf("-p 1 re-raised %v, want %d", want, lowest)
		}
		for _, limit := range []int{2, 4, 16, 64} {
			for range 20 {
				if got := catch(limit, 64, job); got != want {
					t.Fatalf("-p %d re-raised %v, -p 1 re-raised %v",
						limit, got, want)
				}
			}
		}
	}
}

func TestFSRelativeToDir(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	e := New(Options{Dir: dir, Stdout: &out})
	if wd, _ := e.FS.Getwd(); wd != dir {
		t.Fatalf("Getwd = %q, want %q", wd, dir)
	}
	if err := e.FS.MkdirAll("a/b", 0o755); err != nil {
		t.Fatal(err)
	}
	tmp, err := e.FS.WriteTemp("a/b", ".x-", []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(tmp) != filepath.Join(dir, "a", "b") {
		t.Fatalf("temp file %q not in a/b", tmp)
	}
	if err := e.FS.Rename(tmp, "a/b/x.vhd"); err != nil {
		t.Fatal(err)
	}
	b, err := e.FS.ReadFile(filepath.Join(dir, "a/b/x.vhd"))
	if err != nil || string(b) != "new" {
		t.Fatalf("ReadFile = %q, %v", b, err)
	}
	ents, _ := e.FS.ReadDir("a/b")
	if len(ents) != 1 || ents[0].Name() != "x.vhd" {
		t.Fatalf("ReadDir = %v", ents)
	}
	if _, err := e.FS.Stat("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat(missing) = %v", err)
	}
	e.Transport.Stdout.Write([]byte("hi"))
	if out.String() != "hi" {
		t.Fatal("Stdout option ignored")
	}
}

func TestOrderUsesSeed(t *testing.T) {
	e := New(Options{Seed: 5})
	if !slices.Equal(e.Order.Permute(3, 10), env.Shuffle(5, 3, 10)) {
		t.Fatal("Order must be env.Shuffle of the seed")
	}
}

func TestExecutor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	e := New(Options{Dir: t.TempDir()})
	sh, err := e.Executor.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	res, err := e.Executor.Run(context.Background(), env.Command{
		Path: sh, Args: []string{"-c", "cat; echo err >&2; exit 3"},
		Stdin: []byte("in"),
	})
	if err != nil || res.ExitCode != 3 || string(res.Stdout) != "in" ||
		string(res.Stderr) != "err\n" {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Executor.Run(ctx, env.Command{Path: sh,
		Args: []string{"-c", "exit 0"}}); err == nil {
		t.Fatal("Run with a done context must fail")
	}
	if _, err := e.Executor.LookPath("no-such-program-gohard"); err == nil {
		t.Fatal("LookPath of a missing program succeeded")
	}
	if err := e.Executor.Start(env.Command{Path: sh,
		Args: []string{"-c", "exit 0"}}); err != nil {
		t.Fatal(err)
	}
}

func TestClock(t *testing.T) {
	c := New(Options{}).Clock
	start := c.Now()
	tm := c.NewTimer(time.Millisecond)
	<-tm.C()
	if tm.Stop() {
		t.Fatal("a fired timer reported pending")
	}
	if err := c.Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Hour); err == nil {
		t.Fatal("Sleep ignored a done context")
	}
	if !c.Now().After(start) {
		t.Fatal("the clock did not move")
	}
}

// TestGetwdError: when the current directory cannot be found (it was
// deleted), Getwd reports the error instead of "", which would make the
// filesystem root the project root.
func TestGetwdError(t *testing.T) {
	old := getwd
	t.Cleanup(func() { getwd = old })
	want := errors.New("getwd: no such file or directory")
	getwd = func() (string, error) { return "", want }
	e := New(Options{})
	if wd, err := e.FS.Getwd(); !errors.Is(err, want) || wd != "" {
		t.Fatalf("Getwd = %q, %v; want the error", wd, err)
	}
	getwd = old
	if _, err := (osFS{}).Getwd(); err == nil {
		t.Fatal("an empty directory must not be reported as valid")
	}
}

// TestDeletedCwd runs check and config check from a deleted directory:
// one config/unreadable diagnostic, exit 2, and no walk of "/".
func TestDeletedCwd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("removing the current directory needs Linux")
	}
	dir := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"check"}, {"config", "check"}} {
		var out, errb bytes.Buffer
		e := New(Options{Stdout: &out, Stderr: &errb})
		code := cli.Run(context.Background(), e, args)
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if code != 2 || len(lines) != 2 ||
			!strings.Contains(lines[0], "error[config/unreadable]") {
			t.Fatalf("%v: exit %d, stdout:\n%s", args, code, out.String())
		}
	}
}

// TestBrokenPipeMarked: realenv marks EPIPE on stdout and stderr with
// env.ErrBrokenPipe and passes every other error through unmarked.
func TestBrokenPipeMarked(t *testing.T) {
	for _, tc := range []struct {
		err  error
		pipe bool
	}{
		{&fs.PathError{Op: "write", Path: "|1", Err: syscall.EPIPE}, true},
		{syscall.EPIPE, true},
		{&fs.PathError{Op: "write", Path: "x", Err: syscall.ENOSPC}, false},
		{errors.New("closed"), false},
	} {
		e := New(Options{Stdout: errWriter{tc.err},
			Stderr: errWriter{tc.err}})
		for _, w := range []io.Writer{e.Transport.Stdout,
			e.Transport.Stderr} {
			_, err := w.Write([]byte("x"))
			if !errors.Is(err, tc.err) ||
				errors.Is(err, env.ErrBrokenPipe) != tc.pipe {
				t.Errorf("%v: got %v", tc.err, err)
			}
		}
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	defer w.Close()
	if runtime.GOOS != "windows" {
		e := New(Options{Stdout: w})
		_, err := e.Transport.Stdout.Write([]byte("x"))
		if !errors.Is(err, env.ErrBrokenPipe) {
			t.Fatalf("write to a closed pipe: %v", err)
		}
	}
}

type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

// TestWriteTempMode: temp files become 0644, so a file renamed into
// place is not owner-only.
func TestWriteTempMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix modes")
	}
	e := New(Options{Dir: t.TempDir()})
	name, err := e.FS.WriteTemp(".", ".x-", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(name)
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v, %v", fi.Mode(), err)
	}
}

// TestPoolRunsInParallel: at limit k, k jobs are running at the same
// time. Each job waits for all k to arrive; a pool that ran them one by
// one would time out.
func TestPoolRunsInParallel(t *testing.T) {
	for _, k := range []int{2, 4, 8} {
		e := New(Options{Workers: k})
		var arrived atomic.Int64
		all := make(chan struct{})
		var timedOut atomic.Bool
		e.Jobs.Run(k, k, func(int) {
			if arrived.Add(1) == int64(k) {
				close(all)
			}
			select {
			case <-all:
			case <-time.After(10 * time.Second):
				timedOut.Store(true)
			}
		})
		if timedOut.Load() {
			t.Fatalf("limit %d: jobs did not run at the same time", k)
		}
	}
}
