package cli_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/simrun"
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
	// bundleErr lists bundled files (File.Path) whose analysis
	// reports an error through stdlib.WithInternalError.
	bundleErr []string
	// rand, when set, replaces env.Rand with a SplitMix64 of this
	// state, so --seed random is reproducible.
	rand *uint64
	// viewers, when set, replaces env.Executor with a simulated one
	// whose PATH holds exactly these programs (Open waveform, #36).
	viewers *[]string
	// hostOS and hostRoot, when set, make `gohard libs detect` search
	// a host of that OS whose file-system root is the folder hostRoot
	// (libs.WithHost, #1).
	hostOS, hostRoot string
	// build, when set, is the -ldflags values and Go build info of
	// the binary under test (cli.WithBuildInfo, #39).
	build *buildInfo
	// restore lists paths chmod made unreadable; restorePerms makes
	// them readable again after the run.
	restore []string
}

// buildInfo is a `build-info` directive.
type buildInfo struct {
	version, commit string // -ldflags values
	bi              *debug.BuildInfo
}

// parseBuildInfo parses the KEY=VALUE words of a build-info directive:
// ldflags.version and ldflags.commit are the -ldflags values; go,
// main.version and vcs.* fill a debug.BuildInfo, which is nil when none
// of them is given.
func parseBuildInfo(words []string) (*buildInfo, bool) {
	out := &buildInfo{}
	var bi debug.BuildInfo
	seen := false
	for _, w := range words {
		k, v, ok := strings.Cut(w, "=")
		if !ok {
			return nil, false
		}
		switch {
		case k == "ldflags.version":
			out.version = v
		case k == "ldflags.commit":
			out.commit = v
		case k == "go":
			bi.GoVersion, seen = v, true
		case k == "main.version":
			bi.Main.Version, seen = v, true
		case strings.HasPrefix(k, "vcs"):
			bi.Settings = append(bi.Settings,
				debug.BuildSetting{Key: k, Value: v})
			seen = true
		default:
			return nil, false
		}
	}
	if out.version == "" {
		out.version = cli.Version
	}
	if seen {
		out.bi = &bi
	}
	return out, true
}

// restorePerms undoes the chmod directives so the temp dir can be read
// back and removed.
func (st setup) restorePerms(t tb) {
	for _, p := range st.restore {
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// aggregate is a package whose constant aggregate has n+1 elements:
// about 2n syntax-tree nodes in 2n bytes, so a small file can exceed
// the 1,000,000-node limit (#20, #31) without committing it.
func aggregate(n int) []byte {
	return []byte("package big is\n  constant c : integer_vector := (\n" +
		strings.Repeat("0,", n) + "0);\nend package;\n")
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
		case f[0] == "aggregate" && len(f) == 3:
			n, err := strconv.Atoi(f[2])
			if err != nil || n < 1 {
				bad()
			}
			mkparent(t, at(f[1]))
			if err := os.WriteFile(at(f[1]), aggregate(n),
				0o644); err != nil {
				t.Fatal(err)
			}
		case f[0] == "chmod" && len(f) == 3:
			mode, err := strconv.ParseUint(f[2], 8, 32)
			if err != nil {
				bad()
			}
			// Windows has no permission bits: the case cannot be
			// built there.
			if runtime.GOOS == "windows" {
				t.Skip("cannot make a path unreadable here")
			}
			p := at(f[1])
			if err := os.Chmod(p, os.FileMode(mode)); err != nil {
				t.Fatal(err)
			}
			// Restore it so t.TempDir can remove the tree.
			st.restore = append(st.restore, p)
			// Root (or a capability) reads everything: probe the
			// path instead of asking for the user id.
			if readable(p) {
				st.restorePerms(t)
				t.Skip("path stays readable (running as root?)")
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
		case f[0] == "rand" && len(f) == 2:
			v, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil {
				bad()
			}
			st.rand = &v
		case f[0] == "libs-host" && len(f) == 3:
			st.hostOS, st.hostRoot = f[1], at(f[2])
			if err := os.MkdirAll(st.hostRoot, 0o755); err != nil {
				t.Fatal(err)
			}
		case f[0] == "viewers":
			vs := slices.Clone(f[1:])
			st.viewers = &vs
		case line == "requires interpreter":
			if simrun.DefaultCode == nil {
				t.Skip("needs the sequential interpreter " +
					"(simrun.DefaultCode)")
			}
		case line == "stdout broken":
			st.broken = true
		case line == "stdout full":
			st.full = true
		case f[0] == "build-info":
			b, ok := parseBuildInfo(f[1:])
			if !ok {
				bad()
			}
			st.build = b
		case f[0] == "tamper-stdlib" && len(f) == 2:
			st.tamper = append(st.tamper, f[1])
		case f[0] == "stdlib-internal-error" && len(f) == 2:
			st.bundleErr = append(st.bundleErr, f[1])
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
	if st.rand != nil {
		e.Rand = &env.SplitMix64{State: *st.rand}
	}
	if st.viewers != nil {
		ps := map[string]simenv.Program{}
		for _, v := range *st.viewers {
			ps["/viewers/"+v] = func(context.Context, env.Command) (
				env.Result, error,
			) {
				return env.Result{}, nil
			}
		}
		e.Executor = simenv.NewExecutor([]string{"/viewers"}, ps)
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

// readable reports whether p can still be read after a chmod: a
// directory is listed, a file opened and read.
func readable(p string) bool {
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		_, err := os.ReadDir(p)
		return err == nil
	}
	_, err := os.ReadFile(p)
	return err == nil
}
