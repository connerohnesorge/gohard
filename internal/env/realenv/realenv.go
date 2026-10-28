// Package realenv is the real implementation of the internal/env doors:
// the operating system's files, clock, environment, standard streams and
// subprocesses. It is one of the two places (with cmd/) allowed to touch
// the world (P2); detlint exempts it.
package realenv

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// Options configure New. The zero value uses the process's own streams
// and current directory.
type Options struct {
	// Stdin, Stdout and Stderr default to the process's streams.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Dir is the run's current directory; relative FS names resolve
	// against it. It defaults to os.Getwd. Tests set it instead of
	// calling os.Chdir, so runs can happen in parallel.
	Dir string
	// Seed is the in-delta order seed (0 is declaration order).
	Seed uint64
	// Workers is the default job-runner parallelism; <= 0 means
	// GOMAXPROCS.
	Workers int
}

// New returns an Env backed by the operating system.
func New(o Options) env.Env {
	dir, wdErr := o.Dir, error(nil)
	if dir == "" {
		dir, wdErr = getwd()
	}
	if wdErr == nil {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	return env.Env{
		Clock: clock{},
		Rand:  rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
		FS:    osFS{dir: dir, wdErr: wdErr},
		Transport: env.Transport{
			Stdin: orDefault[io.Reader](o.Stdin, os.Stdin),
			Stdout: pipeWriter{orDefault[io.Writer](o.Stdout,
				os.Stdout)},
			Stderr: pipeWriter{orDefault[io.Writer](o.Stderr,
				os.Stderr)},
		},
		Executor: executor{dir: dir},
		Getenv:   os.LookupEnv,
		Jobs:     pool{workers: o.Workers},
		Order:    env.SeedOrder{Seed: o.Seed},
	}
}

// pipeWriter marks a write to a closed pipe with env.ErrBrokenPipe, so
// the shell can exit quietly on | head yet fail on any other write error
// (a full disk, a closed descriptor).
type pipeWriter struct{ w io.Writer }

func (p pipeWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	if err != nil && isBrokenPipe(err) {
		err = fmt.Errorf("%w: %w", env.ErrBrokenPipe, err)
	}
	return n, err
}

// Windows reports a closed pipe as ERROR_BROKEN_PIPE or ERROR_NO_DATA.
const (
	errorBrokenPipe syscall.Errno = 109
	errorNoData     syscall.Errno = 232
)

func isBrokenPipe(err error) bool {
	if errors.Is(err, syscall.EPIPE) {
		return true
	}
	var no syscall.Errno
	return runtime.GOOS == "windows" && errors.As(err, &no) &&
		(no == errorBrokenPipe || no == errorNoData)
}

func orDefault[T any](v T, def T) T {
	if any(v) == nil {
		return def
	}
	return v
}

type clock struct{}

func (clock) Now() time.Time { return time.Now() }

func (clock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (clock) NewTimer(d time.Duration) env.Timer {
	return timer{time.NewTimer(d)}
}

type timer struct{ t *time.Timer }

func (t timer) C() <-chan time.Time { return t.t.C }
func (t timer) Stop() bool          { return t.t.Stop() }

// getwd is os.Getwd; tests replace it to simulate a deleted current
// directory.
var getwd = os.Getwd

// osFS resolves relative names against dir. wdErr is the error from
// finding the current directory: when it is set, Getwd reports it, so
// callers never mistake "" for a real directory (an empty dir would make
// the filesystem root the project root).
type osFS struct {
	dir   string
	wdErr error
}

// abs makes name absolute and cleans it lexically, so lnk/../x is x
// whether the name is absolute or relative, exactly as simenv resolves
// it; the OS would otherwise apply ".." after following lnk.
func (f osFS) abs(name string) string {
	return env.Abs(f.dir, name)
}

func (f osFS) Getwd() (string, error) {
	if f.wdErr != nil {
		return "", f.wdErr
	}
	if f.dir == "" {
		return "", errors.New("current directory unknown")
	}
	return f.dir, nil
}

func (f osFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(f.abs(name))
}

func (f osFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(f.abs(name), data, perm)
}

func (f osFS) WriteTemp(dir, prefix string, data []byte) (string, error) {
	t, err := os.CreateTemp(f.abs(dir), prefix+"*.tmp")
	if err != nil {
		return "", err
	}
	// CreateTemp makes the file 0600; a report or repro file renamed
	// into place should be readable like any file the user writes.
	merr := t.Chmod(0o644)
	_, werr := t.Write(data)
	cerr := t.Close()
	if err := cmp.Or(merr, werr, cerr); err != nil {
		os.Remove(t.Name())
		return "", err
	}
	return t.Name(), nil
}

func (f osFS) Rename(oldname, newname string) error {
	return os.Rename(f.abs(oldname), f.abs(newname))
}

func (f osFS) Chmod(name string, perm fs.FileMode) error {
	return os.Chmod(f.abs(name), perm)
}

func (f osFS) Remove(name string) error { return os.Remove(f.abs(name)) }

func (f osFS) MkdirAll(name string, perm fs.FileMode) error {
	return os.MkdirAll(f.abs(name), perm)
}

func (f osFS) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(f.abs(name))
}

func (f osFS) Lstat(name string) (fs.FileInfo, error) {
	return os.Lstat(f.abs(name))
}

func (f osFS) Readlink(name string) (string, error) {
	return os.Readlink(f.abs(name))
}

func (f osFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(f.abs(name)) // sorted by name
}

func (f osFS) Open(name string) (io.ReadCloser, error) {
	return os.Open(f.abs(name))
}

func (f osFS) Create(name string) (io.WriteCloser, error) {
	return os.Create(f.abs(name))
}

func (f osFS) Append(name string) (io.WriteCloser, error) {
	return os.OpenFile(f.abs(name),
		os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
}

type executor struct{ dir string }

func (executor) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (x executor) command(ctx context.Context, c env.Command) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Dir = cmp.Or(c.Dir, x.dir)
	return cmd
}

func (x executor) Run(ctx context.Context, c env.Command) (
	env.Result, error,
) {
	cmd := x.command(ctx, c)
	var stdout, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(c.Stdin)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := env.Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok &&
		ctx.Err() == nil {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	if err != nil && ctx.Err() != nil {
		return res, ctx.Err()
	}
	return res, err
}

// Start launches the command detached. A goroutine waits for it so the
// finished child is reaped instead of lingering as a zombie.
func (x executor) Start(c env.Command) error {
	cmd := x.command(context.Background(), c)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// pool runs jobs on a bounded set of goroutines. A panicking job is
// re-panicked on the caller's goroutine after every worker stopped, so
// the CLI's crash handler sees it.
type pool struct{ workers int }

func (p pool) Run(limit, n int, job func(i int)) {
	if limit <= 0 {
		limit = p.workers
	}
	if limit <= 0 {
		limit = runtime.GOMAXPROCS(0)
	}
	limit = min(limit, n)
	if limit <= 1 {
		env.Sequential{}.Run(1, n, job)
		return
	}
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		next   int
		low    = n // lowest index that panicked; n means none
		lowVal any
	)
	// run calls job(i) and records its panic. Indices are handed out in
	// increasing order and stop after the first panic, so every index
	// below a panicking one has been started; after wg.Wait the lowest
	// panicking index is the one env.Sequential would have raised (the
	// env.Jobs contract), whatever the scheduling or limit.
	run := func(i int) (ok bool) {
		defer func() {
			if !ok {
				r := recover()
				mu.Lock()
				if i < low {
					low, lowVal = i, r
				}
				next = n // stop handing out jobs
				mu.Unlock()
			}
		}()
		job(i)
		return true
	}
	for range limit {
		wg.Go(func() {
			for {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= n || !run(i) {
					return
				}
			}
		})
	}
	wg.Wait()
	if low < n {
		panic(lowVal)
	}
}
