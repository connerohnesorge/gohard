package cli_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/env"
)

// setup is the parsed testdata/cli/<case>/setup file: world changes the
// in/ tree cannot express (symbolic links, huge files, a .git folder,
// environment variables, a closed pipe, an injected crash). See
// testdata/cli/README.md.
type setup struct {
	cwd     string            // run directory, relative to the work dir
	env     map[string]string // environment overrides
	broken  bool              // stdout is a closed pipe
	full    bool              // stdout fails like a full disk
	panicOn []string          // slash paths whose ReadFile panics
	// interrupt is the signal delivered (the context is cancelled with
	// it as the cause) when a file whose slash path ends in intrOn is
	// read; nil means none.
	interrupt *env.Interrupt
	intrOn    string
	// tamper lists bundled std/ieee files (stdlib.File paths) whose
	// integrity check fails, through the stdlib.WithTamper test hook.
	tamper []string
}

func applySetup(t tb, dir, work string) setup {
	t.Helper()
	st := setup{cwd: ".", env: map[string]string{}}
	raw, err := os.ReadFile(filepath.Join(dir, "setup"))
	if os.IsNotExist(err) {
		return st
	}
	if err != nil {
		t.Fatal(err)
	}
	at := func(p string) string {
		return filepath.Join(work, filepath.FromSlash(p))
	}
	for n, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		bad := func() {
			t.Fatalf("setup:%d: bad directive %q", n+1, line)
		}
		switch {
		case f[0] == "symlink" && len(f) == 3:
			mkparent(t, at(f[1]))
			if err := os.Symlink(filepath.FromSlash(f[2]),
				at(f[1])); err != nil {
				// Windows needs developer mode or elevation, which CI
				// runners have: skip only outside CI, so the CI gate
				// really runs the symlink cases on every OS (#20).
				if _, ci := os.LookupEnv("CI"); runtime.GOOS ==
					"windows" && !ci {
					t.Skip("cannot create symbolic links: ", err)
				}
				t.Fatal(err)
			}
		case f[0] == "size" && len(f) == 3:
			size, err := strconv.ParseInt(f[2], 10, 64)
			if err != nil {
				bad()
			}
			mkparent(t, at(f[1]))
			fh, err := os.OpenFile(at(f[1]), os.O_CREATE|os.O_WRONLY,
				0o644)
			if err == nil {
				err = errors.Join(fh.Truncate(size), fh.Close())
			}
			if err != nil {
				t.Fatal(err)
			}
		case f[0] == "mkdir" && len(f) == 2:
			if err := os.MkdirAll(at(f[1]), 0o755); err != nil {
				t.Fatal(err)
			}
		case f[0] == "cwd" && len(f) == 2:
			st.cwd = f[1]
		case f[0] == "env" && len(f) == 2:
			k, v, ok := strings.Cut(f[1], "=")
			if !ok {
				bad()
			}
			st.env[k] = v
		case line == "stdout broken":
			st.broken = true
		case line == "stdout full":
			st.full = true
		case f[0] == "tamper-stdlib" && len(f) == 2:
			st.tamper = append(st.tamper, f[1])
		case f[0] == "panic-on-read" && len(f) == 2:
			st.panicOn = append(st.panicOn, f[1])
		case f[0] == "interrupt" && len(f) == 4 && f[2] == "on-read":
			sig := map[string]env.Interrupt{"SIGINT": env.SIGINT,
				"SIGTERM": env.SIGTERM}
			i, ok := sig[f[1]]
			if !ok {
				bad()
			}
			st.interrupt, st.intrOn = &i, f[3]
		default:
			bad()
		}
	}
	return st
}

func mkparent(t tb, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
}

// wrap applies the run-time parts of the setup to e; cancel cancels the
// run's context.
func (st setup) wrap(e *env.Env, cancel context.CancelCauseFunc) {
	if len(st.env) > 0 {
		real := e.Getenv
		e.Getenv = func(k string) (string, bool) {
			if v, ok := st.env[k]; ok {
				return v, true
			}
			return real(k)
		}
	}
	if st.broken {
		e.Transport.Stdout = brokenPipe{}
	}
	if st.full {
		e.Transport.Stdout = fullDisk{}
	}
	if len(st.panicOn) > 0 {
		e.FS = panicFS{FS: e.FS, on: st.panicOn}
	}
	if st.interrupt != nil {
		e.FS = interruptFS{FS: e.FS, on: st.intrOn,
			fire: func() { cancel(*st.interrupt) }}
	}
}

// interruptFS delivers a signal, as the shell does on Ctrl-C or
// SIGTERM, while a listed file is read; the read itself succeeds.
type interruptFS struct {
	env.FS
	on   string
	fire func()
}

func (p interruptFS) ReadFile(name string) ([]byte, error) {
	if strings.HasSuffix(filepath.ToSlash(name), "/"+p.on) {
		p.fire()
	}
	return p.FS.ReadFile(name)
}

// brokenPipe fails like realenv's stdout after the reader went away.
type brokenPipe struct{}

func (brokenPipe) Write([]byte) (int, error) {
	return 0, fmt.Errorf("%w: %w", env.ErrBrokenPipe,
		&os.PathError{Op: "write", Path: "|1",
			Err: errors.New("broken pipe")})
}

// fullDisk fails like a write to /dev/full.
type fullDisk struct{}

func (fullDisk) Write([]byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: "/dev/stdout",
		Err: errors.New("no space left on device")}
}

var (
	_ io.Writer = brokenPipe{}
	_ io.Writer = fullDisk{}
)

// panicFS panics when a listed file is read, to reach the crash path.
type panicFS struct {
	env.FS
	on []string
}

func (p panicFS) ReadFile(name string) ([]byte, error) {
	for _, o := range p.on {
		if strings.HasSuffix(filepath.ToSlash(name), "/"+o) {
			panic("fixture: injected panic reading " + o)
		}
	}
	return p.FS.ReadFile(name)
}
