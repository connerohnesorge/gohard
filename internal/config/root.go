package config

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
)

// Found says how FindRoot chose the project root.
type Found uint8

// How the root was found.
const (
	// FoundConfig: the nearest ancestor holding gohard.yaml.
	FoundConfig Found = iota + 1
	// FoundGit: no ancestor holds gohard.yaml; the nearest one holding
	// .git (a directory, or a file for worktrees and submodules).
	FoundGit
	// FoundStart: neither was found; the start directory itself (the
	// current directory for the CLI, the workspace folder for the
	// language server).
	FoundStart
)

// String names the rule for logs and tests.
func (f Found) String() string {
	switch f {
	case FoundConfig:
		return "gohard.yaml"
	case FoundGit:
		return ".git"
	case FoundStart:
		return "start"
	}
	return "unknown"
}

// FindRoot returns the project root for start, an absolute directory
// (#20, #28): the nearest ancestor (start included) that contains
// gohard.yaml; else the nearest that contains .git; else start. An entry
// that exists but cannot be inspected counts as present, so a
// gohard.yaml without read permission is reported instead of skipped.
// Entries are probed with Lstat: a dangling gohard.yaml symbolic link is
// present too, so Load reports it as config/unreadable (exit 2) instead
// of silently using an ancestor's gohard.yaml. This is the only
// project-root rule; every command and the language server use it.
func FindRoot(fsys env.FS, start string) (string, Found) {
	start = filepath.Clean(start)
	if dir, ok := nearest(fsys, start, FileName); ok {
		return dir, FoundConfig
	}
	if dir, ok := nearest(fsys, start, ".git"); ok {
		return dir, FoundGit
	}
	return start, FoundStart
}

func nearest(fsys env.FS, start, name string) (string, bool) {
	for dir := start; ; {
		if _, err := fsys.Lstat(filepath.Join(dir, name)); err == nil ||
			!errors.Is(err, fs.ErrNotExist) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Loaded is the outcome of Load.
type Loaded struct {
	// Root is the absolute project root.
	Root  string
	Found Found
	// Present reports whether Root holds a gohard.yaml.
	Present bool
	Config  Config
	// Source is the text of gohard.yaml when it was read, for
	// renderers that need the line text (SARIF columns).
	Source []byte
	// Diagnostics are sorted; their paths are relative to Root.
	Diagnostics []diag.Diagnostic
}

// Valid reports whether the configuration can be used: no diagnostic
// is a tool or setup error. Commands that load an invalid configuration
// exit 2 and print the diagnostics (#20).
func (l *Loaded) Valid() bool {
	for _, d := range l.Diagnostics {
		if d.Class() == diag.Tool {
			return false
		}
	}
	return true
}

// Load finds the project root for start (an absolute directory, or
// relative to fsys.Getwd; "" means the current directory), reads and
// parses its gohard.yaml, and checks that every library folder can be
// read. A missing gohard.yaml gives Defaults and no diagnostic.
func Load(fsys env.FS, start string) Loaded {
	if start == "" || !env.IsRooted(start) {
		wd, err := fsys.Getwd()
		if err != nil {
			return Loaded{Root: start, Found: FoundStart,
				Config: Defaults(),
				Diagnostics: []diag.Diagnostic{diag.New(
					diag.ConfigUnreadable, ".", reason(err))},
			}
		}
		start = env.Abs(wd, start)
	}
	root, found := FindRoot(fsys, start)
	l := Loaded{Root: root, Found: found, Config: Defaults()}
	if found != FoundConfig {
		return l
	}
	l.Present = true
	name := filepath.Join(root, FileName)
	src, err := readFile(fsys, name)
	if err != nil {
		l.Diagnostics = []diag.Diagnostic{diag.New(
			diag.ConfigUnreadable, FileName, reason(err)).At(
			FileName, 0, 0)}
		return l
	}
	l.Source = src
	l.Config, l.Diagnostics = Parse(FileName, src)
	l.Diagnostics = diag.Normalize(append(l.Diagnostics,
		CheckFolders(fsys, root, &l.Config)...))
	return l
}

var (
	errIsDir      = errors.New("it is a directory")
	errDangling   = errors.New("it is a symbolic link to a missing file")
	errNotRegular = errors.New("it is not a regular file")
	errTooLarge   = errors.New("it is larger than " +
		strconv.Itoa(MaxSize) + " bytes")
)

func readFile(fsys env.FS, name string) ([]byte, error) {
	fi, err := fsys.Stat(name)
	switch {
	case err == nil && fi.IsDir():
		return nil, errIsDir
	case err == nil && !fi.Mode().IsRegular():
		// A FIFO would block forever, /dev/zero never ends.
		return nil, errNotRegular
	case err == nil && fi.Size() > MaxSize:
		return nil, errTooLarge
	case errors.Is(err, fs.ErrNotExist):
		// FindRoot saw the name with Lstat, so it is a dangling link.
		return nil, errDangling
	}
	b, err := fsys.ReadFile(name)
	if err == nil && len(b) > MaxSize {
		return nil, errTooLarge // it grew after Stat
	}
	return b, err
}

// MaxSize is the largest gohard.yaml Load reads, in bytes. It matches
// the source file limit of #20 (10 MB).
const MaxSize = 10_000_000

// reason turns a file error into text that is the same on every OS and
// never contains an absolute path (#20 determinism).
func reason(err error) string {
	switch {
	case errors.Is(err, errIsDir), errors.Is(err, errDangling),
		errors.Is(err, errNotRegular), errors.Is(err, errTooLarge):
		return err.Error()
	case errors.Is(err, fs.ErrNotExist):
		return "it does not exist"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// CheckFolders reports every library folder that is missing or cannot
// be listed (library/folder-missing, exit 2). Relative folders are
// resolved against root.
func CheckFolders(fsys env.FS, root string, c *Config) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, lib := range c.Libraries {
		for _, f := range lib.Folders {
			p := env.Abs(root, filepath.FromSlash(f.Path))
			if _, err := fsys.ReadDir(p); err != nil {
				out = append(out, diag.New(diag.LibraryFolderMissing,
					lib.Name, f.Path).At(FileName, f.Line, f.Col))
			}
		}
	}
	return out
}
