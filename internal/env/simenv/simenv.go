// Package simenv is the simulated implementation of the internal/env
// doors (#29). Everything it does is a function of its Options, above
// all the seed: a virtual clock and timers, an in-memory file system, a
// splitmix64 random source, scripted standard streams, a fake
// subprocess executor with a configurable PATH, getenv from a map,
// jobs run one at a time in a seed-determined order, and seeded fault
// injection (P6): short and failing reads, files vanishing mid-walk and
// failing writes.
//
// The same Options and the same sequence of calls always give the same
// results, so tests, replay and fuzzing are reproducible (P1). simenv
// touches nothing outside the process: no os, time.Now, timers,
// math/rand, goroutines or select.
//
// Paths are slash-separated. Relative names resolve against
// Options.Dir.
package simenv

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/connerohnesorge/gohard/internal/env"
)

// Epoch is the default start of the virtual clock, the same instant as
// the default `simulation.env.epoch` (#18, #28).
var Epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// DefaultDir is the default current directory of a simulated run.
const DefaultDir = "/work"

// Options configure New. The zero value is a valid, empty environment
// at seed 0.
type Options struct {
	// Seed determines the in-delta order, the job order, the Rand door
	// and every injected fault. 0 keeps declaration order for processes
	// and jobs.
	Seed uint64
	// Epoch is the virtual clock's start; zero means Epoch.
	Epoch time.Time
	// Dir is the current directory, created if missing; "" means
	// DefaultDir.
	Dir string
	// Files are created before the run, with parent directories. A
	// relative name is relative to Dir.
	Files map[string][]byte
	// Stdin is the scripted standard input. Each Read returns at most
	// one chunk, so tests control how input is split across reads.
	Stdin [][]byte
	// Vars are the environment variables seen through Getenv.
	Vars map[string]string
	// Path is the executable search path of Executor.LookPath; nil
	// means the colon-separated PATH entry of Vars.
	Path []string
	// Programs are the executables the Executor knows, by absolute
	// path.
	Programs map[string]Program
	// Faults are the seeded fault rates of the FS.
	Faults Faults
}

// Sim is one simulated environment. Its parts stay inspectable after
// the run.
type Sim struct {
	Clock    *Clock
	FS       *FS
	Executor *Executor
	Stdout   *Output
	Stderr   *Output
	Jobs     *Jobs

	env env.Env
}

// Salts separate the generator streams drawn from one seed.
const (
	randSalt  = 0x52414e44a5a5a5a5
	faultSalt = 0x4641554c545a5a5a
	jobsSalt  = 0x4a4f4253c3c3c3c3
)

// New builds a simulated environment. It panics if Files cannot be
// created (for example a file listed below another file), since that is
// a bug in the test that wrote the Options.
func New(o Options) *Sim {
	epoch := o.Epoch
	if epoch.IsZero() {
		epoch = Epoch
	}
	clock := NewClock(epoch)
	fsys := NewFS(clock, o.Dir, o.Seed, o.Faults)
	for _, name := range sortedKeys(o.Files) {
		if err := fsys.put(name, o.Files[name]); err != nil {
			panic("simenv: " + err.Error())
		}
	}
	vars := make(map[string]string, len(o.Vars))
	for k, v := range o.Vars {
		vars[k] = v
	}
	path := o.Path
	if path == nil {
		path = splitPath(vars["PATH"])
	}
	s := &Sim{
		Clock:    clock,
		FS:       fsys,
		Executor: NewExecutor(path, o.Programs),
		Stdout:   &Output{},
		Stderr:   &Output{},
		Jobs:     &Jobs{seed: o.Seed},
	}
	s.env = env.Env{
		Clock: clock,
		Rand:  &env.SplitMix64{State: o.Seed ^ randSalt},
		FS:    fsys,
		Transport: env.Transport{
			Stdin:  &Script{chunks: slices.Clone(o.Stdin)},
			Stdout: s.Stdout,
			Stderr: s.Stderr,
		},
		Executor: s.Executor,
		Getenv:   env.LookupFunc(vars),
		Jobs:     s.Jobs,
		Order:    env.SeedOrder{Seed: o.Seed},
	}
	return s
}

// Env returns the doors of s. Every call returns the same doors.
func (s *Sim) Env() env.Env { return s.env }

func splitPath(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, ":")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Jobs is the simulated job runner: it calls every job on the calling
// goroutine, in an order drawn from the seed and a per-call counter
// (index order at seed 0). Code whose output depends on job order shows
// up as a seed-dependent difference in tests.
type Jobs struct {
	mu    sync.Mutex
	seed  uint64
	calls uint64
}

// Run implements env.Jobs. limit is ignored: jobs never overlap.
func (j *Jobs) Run(_, n int, job func(i int)) {
	j.mu.Lock()
	step := j.calls
	j.calls++
	j.mu.Unlock()
	// Every job runs; the panic of the lowest index is re-raised, the
	// one env.Sequential would raise (the env.Jobs contract), so the
	// shuffled order never changes which crash is reported.
	low, lowVal := n, any(nil)
	for _, i := range env.Shuffle(j.seed, step^jobsSalt, n) {
		func() {
			ok := false
			defer func() {
				if !ok {
					if r := recover(); i < low {
						low, lowVal = i, r
					}
				}
			}()
			job(i)
			ok = true
		}()
	}
	if low < n {
		panic(lowVal)
	}
}

// Script is a scripted reader: each Read returns bytes from one chunk
// only, then io.EOF after the last chunk.
type Script struct {
	mu     sync.Mutex
	chunks [][]byte
}

// NewScript returns a Script over copies of chunks.
func NewScript(chunks ...[]byte) *Script {
	return &Script{chunks: slices.Clone(chunks)}
}

// Read implements io.Reader.
func (s *Script) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.chunks) > 0 && len(s.chunks[0]) == 0 {
		s.chunks = s.chunks[1:]
	}
	if len(s.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.chunks[0])
	s.chunks[0] = s.chunks[0][n:]
	return n, nil
}

// Output records everything written to a simulated stream.
type Output struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer; it never fails.
func (o *Output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

// Bytes returns a copy of everything written so far.
func (o *Output) Bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return bytes.Clone(o.buf.Bytes())
}

// String returns everything written so far.
func (o *Output) String() string { return string(o.Bytes()) }
