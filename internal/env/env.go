// Package env is the only door through which the outside world reaches
// GoHard (P2, #29). One Env value carries every door: Clock, Rand, FS,
// Transport, Executor, Getenv, the job runner and the in-delta process
// order. Core packages take what they need from an Env and never call
// time.Now, math/rand, os, net or os/exec themselves; detlint enforces
// this in CI (#30).
//
// There are two implementations: internal/env/realenv (used only by
// cmd/ and by tests that need the real filesystem) and
// internal/env/simenv (fully determined by its seed; used by tests,
// replay and fuzzing). This package holds the interfaces and a few pure
// helpers that both share.
package env

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"time"
)

// Env is the set of doors. Every field must be non-nil; a zero Env is
// not usable.
type Env struct {
	Clock     Clock
	Rand      Rand
	FS        FS
	Transport Transport
	Executor  Executor
	Getenv    Getenv
	Jobs      Jobs
	Order     Order
}

// Clock is wall-clock time, sleeps and timers. Cores never read it
// directly: in the server and simulator, timer fires are events fed to
// the pure Step core by the shell (P3). Simulated time is a value in the
// kernel and never comes from here.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Sleep waits for d or until ctx is done, returning ctx.Err() in the
	// second case.
	Sleep(ctx context.Context, d time.Duration) error
	// NewTimer starts a timer that fires once after d.
	NewTimer(d time.Duration) Timer
}

// Timer is a one-shot timer from Clock.NewTimer.
type Timer interface {
	// C delivers the fire time once.
	C() <-chan time.Time
	// Stop prevents the timer from firing; it reports whether it was
	// still pending.
	Stop() bool
}

// Rand is the only source of randomness (for example `--seed random`).
// Seeded simulation randomness (`uniform`) is derived from the run's
// seed with SplitMix64, not from this door, so it replays exactly.
type Rand interface {
	Uint64() uint64
}

// FS is file access, including the simulator's textio files (#35).
// Names are OS paths; a relative name is resolved against Getwd. Errors
// wrap the io/fs sentinels (fs.ErrNotExist, fs.ErrExist,
// fs.ErrPermission) so callers can test them with errors.Is.
type FS interface {
	// Getwd returns the absolute current directory of the run.
	Getwd() (string, error)
	// ReadFile returns the whole content of name.
	ReadFile(name string) ([]byte, error)
	// WriteFile creates or truncates name and writes data.
	WriteFile(name string, data []byte, perm fs.FileMode) error
	// WriteTemp creates a new file in dir whose name starts with prefix,
	// writes data to it, and returns its name. Together with Rename this
	// replaces a file without ever leaving it half-written (#20, #32).
	WriteTemp(dir, prefix string, data []byte) (string, error)
	// Rename atomically replaces newname with oldname where the OS
	// allows it.
	Rename(oldname, newname string) error
	// Remove deletes a file or an empty directory.
	Remove(name string) error
	// MkdirAll creates a directory and any missing parents.
	MkdirAll(name string, perm fs.FileMode) error
	// Stat follows symbolic links.
	Stat(name string) (fs.FileInfo, error)
	// Lstat does not follow a final symbolic link.
	Lstat(name string) (fs.FileInfo, error)
	// Readlink returns the target of a symbolic link.
	Readlink(name string) (string, error)
	// ReadDir returns the entries of a directory sorted by name.
	ReadDir(name string) ([]fs.DirEntry, error)
	// Open opens a file for streaming reads (textio).
	Open(name string) (io.ReadCloser, error)
	// Create creates or truncates a file for streaming writes (textio
	// write_mode).
	Create(name string) (io.WriteCloser, error)
	// Append opens a file for appending, creating it if needed (textio
	// append_mode).
	Append(name string) (io.WriteCloser, error)
}

// Transport is the process's standard streams: the language server's
// JSON-RPC stream and the CLI's input and output. Only jsonrpc2's
// reader and writer goroutines and the CLI shell touch them.
type Transport struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Executor looks up and launches subprocesses: `gohard libs detect`
// (#1) and the waveform viewer (#36).
type Executor interface {
	// LookPath finds an executable on PATH like exec.LookPath.
	LookPath(file string) (string, error)
	// Run runs cmd to completion and returns its exit status and output.
	// A non-zero exit is not an error; failing to start is.
	Run(ctx context.Context, cmd Command) (Result, error)
	// Start launches cmd detached (a waveform viewer) and returns as
	// soon as it started.
	Start(cmd Command) error
}

// Command is one subprocess invocation.
type Command struct {
	Path  string   // executable, usually from LookPath
	Args  []string // arguments, without the program name
	Dir   string   // working directory; "" means Getwd
	Stdin []byte   // standard input for Run
}

// Result is the outcome of Executor.Run.
type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// Getenv reads an environment variable. ok is false when it is unset.
type Getenv func(key string) (value string, ok bool)

// Jobs runs independent pure jobs, such as parsing the files of a
// snapshot (`-p N`). The real runner uses a bounded goroutine pool; the
// simulated one runs jobs one at a time in a seed-determined order.
// Callers store result i in slot i and merge in index order, so output
// never depends on the runner (P3): `-p 1` and `-p 16` give identical
// bytes.
type Jobs interface {
	// Run calls job(i) once for every i in [0, n) and returns when all
	// calls have returned. At most limit jobs run at once; limit <= 0
	// means the runner's default. Jobs must not share mutable state
	// except their own result slot.
	//
	// If jobs panic, Run panics after every job it started has returned,
	// with the value of the lowest-index job that panicked: the panic
	// Sequential raises. Which crash is reported therefore never depends
	// on the runner, the limit or the scheduling. Jobs above that index
	// may or may not run.
	Run(limit, n int, job func(i int))
}

// Order is the in-delta process order of the simulator (#29, #35).
type Order interface {
	// Permute returns the order in which to run n processes that are
	// ready in the same delta cycle. step identifies the delta (for
	// example a running delta counter) so the order is a pure function
	// of (seed, step, n) and a forked or replayed run sees the same
	// order. The result is a permutation of [0, n).
	Permute(step uint64, n int) []int
}

// Interrupt is the context cancellation cause the shell uses when the
// process receives a signal, so commands can tell an interrupt from an
// ordinary cancel with context.Cause.
type Interrupt struct {
	Signal string // "SIGINT" or "SIGTERM"
	Exit   int    // 130 or 143
}

func (i Interrupt) Error() string { return i.Signal }

// The two interrupts of #20.
var (
	SIGINT  = Interrupt{Signal: "SIGINT", Exit: 130}
	SIGTERM = Interrupt{Signal: "SIGTERM", Exit: 143}
)

// ErrBrokenPipe marks a Transport write that failed because the reader
// went away (| head). The shell exits quietly with the run's own code on
// it; any other stdout write error is exit 2 (#20).
var ErrBrokenPipe = errors.New("broken pipe")
