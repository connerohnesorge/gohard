package simenv

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/connerohnesorge/gohard/internal/env"
)

// ErrNotFound is returned by LookPath, like exec.ErrNotFound.
var ErrNotFound = errors.New("executable file not found in $PATH")

// Program is the behaviour of a fake executable.
type Program func(ctx context.Context, cmd env.Command) (env.Result, error)

// Executor is a fake env.Executor: programs live in a table keyed by
// absolute path and are found through a configurable PATH. It records
// every Run and Start, and Start never runs anything.
type Executor struct {
	mu       sync.Mutex
	path     []string
	programs map[string]Program
	calls    []Call
}

// Call is one recorded Run or Start.
type Call struct {
	Start   bool // Start rather than Run
	Command env.Command
}

// NewExecutor returns an executor searching the directories of path in
// order over programs.
func NewExecutor(path []string, programs map[string]Program) *Executor {
	return &Executor{
		path:     slices.Clone(path),
		programs: maps.Clone(programs),
	}
}

// LookPath implements env.Executor. A name with a slash is used as is;
// any other name is searched in the PATH directories in order. An empty
// PATH entry means the current directory, as in a shell; it only
// matches programs registered under a relative name.
func (x *Executor) LookPath(file string) (string, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if strings.Contains(file, "/") {
		if x.programs[file] != nil {
			return file, nil
		}
		return "", &fs.PathError{Op: "exec", Path: file,
			Err: fs.ErrNotExist}
	}
	for _, dir := range x.path {
		p := file
		if dir != "" {
			p = path.Join(dir, file)
		}
		if x.programs[p] != nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("exec: %q: %w", file, ErrNotFound)
}

// Run implements env.Executor by calling the program registered at
// cmd.Path.
func (x *Executor) Run(ctx context.Context, cmd env.Command) (
	env.Result, error,
) {
	p, err := x.record(false, cmd)
	if err != nil {
		return env.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return env.Result{}, err
	}
	return p(ctx, cmd)
}

// Start implements env.Executor: it records cmd and checks that the
// program exists.
func (x *Executor) Start(cmd env.Command) error {
	_, err := x.record(true, cmd)
	return err
}

func (x *Executor) record(start bool, cmd env.Command) (Program, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	cmd.Args = slices.Clone(cmd.Args)
	cmd.Stdin = slices.Clone(cmd.Stdin)
	x.calls = append(x.calls, Call{Start: start, Command: cmd})
	p := x.programs[cmd.Path]
	if p == nil {
		return nil, &fs.PathError{Op: "fork/exec", Path: cmd.Path,
			Err: fs.ErrNotExist}
	}
	return p, nil
}

// Calls returns every recorded Run and Start in call order.
func (x *Executor) Calls() []Call {
	x.mu.Lock()
	defer x.mu.Unlock()
	return slices.Clone(x.calls)
}
