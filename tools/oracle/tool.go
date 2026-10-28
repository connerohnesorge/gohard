package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// tool is an external simulator found on PATH (the Nix dev shell pins
// both).
type tool struct {
	name    string // "ghdl" or "nvc"
	path    string
	version string
	// fake, when set, stands in for the executable (tests).
	fake func(dir string, args []string) (string, int)
}

// toolset finds each tool once.
type toolset struct {
	found map[string]*tool
}

// get returns the named tool, or an error naming the dev shell.
func (s *toolset) get(name string) (*tool, error) {
	if t, ok := s.found[name]; ok {
		return t, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%s not found; run inside the dev shell: "+
			"nix develop -c go run ./tools/oracle --update", name)
	}
	t := &tool{name: name, path: p}
	out, _, err := t.run("", "--version")
	if err != nil {
		return nil, err
	}
	t.version, err = parseVersion(name, out)
	if err != nil {
		return nil, err
	}
	if s.found == nil {
		s.found = map[string]*tool{}
	}
	s.found[name] = t
	return t, nil
}

// parseVersion reads the version from the first line of --version:
// "GHDL 6.0.0 (...) [...]" or "nvc 1.23.0 (...)".
func parseVersion(name, out string) (string, error) {
	line, _, _ := strings.Cut(out, "\n")
	f := strings.Fields(line)
	if len(f) < 2 || !strings.EqualFold(f[0], name) {
		return "", fmt.Errorf("%s --version: unexpected %q", name, line)
	}
	return f[1], nil
}

// run runs the tool in dir and returns its combined output and exit
// code. A non-zero exit is not an error; failing to start is.
func (t *tool) run(dir string, args ...string) (string, int, error) {
	if t.fake != nil {
		out, code := t.fake(dir, args)
		return out, code, nil
	}
	cmd := exec.Command(t.path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NVC_COLORS=never", "TERM=dumb")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), ee.ExitCode(), nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("%s: %w", t.name, err)
	}
	return out.String(), 0, nil
}

// workDir is a fresh temporary work directory holding a set of
// source files at their relative paths.
type workDir struct{ dir string }

func newWorkDir(files map[string][]byte, order []string) (*workDir,
	error) {
	dir, err := os.MkdirTemp("", "gohard-oracle-")
	if err != nil {
		return nil, err
	}
	w := &workDir{dir: dir}
	for _, rel := range order {
		name := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			w.close()
			return nil, err
		}
		if err := os.WriteFile(name, files[rel], 0o644); err != nil {
			w.close()
			return nil, err
		}
	}
	return w, nil
}

// addInputs copies the input files of d into the work directory, at
// their design-relative paths, as the design reads them while it runs.
func (w *workDir) addInputs(x *ctx, d design) error {
	files, err := inputFiles(x, d)
	if err != nil {
		return err
	}
	for _, rel := range d.Inputs {
		name := filepath.Join(w.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(name, files[rel], 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (w *workDir) close() { os.RemoveAll(w.dir) }
