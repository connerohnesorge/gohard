package repro

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
)

// Check is the result of comparing a header with the project as it is
// now.
type Check struct {
	// Changed lists, sorted, every recorded source, input or
	// gohard.yaml whose content differs or that is gone, an input
	// recorded as absent that now exists, and gohard.yaml if it
	// appeared.
	Changed []string
	// Recorded and Current are the recording and the running gohard
	// versions.
	Recorded, Current string
	// Real reports that the run was recorded with --real-env (#18): it
	// read the real clock or environment and is never replayed.
	Real bool
}

// OK reports whether the sources match and the run is reproducible,
// so it can be replayed.
func (c Check) OK() bool { return len(c.Changed) == 0 && !c.Real }

// VersionMismatch reports whether another gohard version recorded the
// run: the run is replayed with a warning and its trace diffed (#29).
func (c Check) VersionMismatch() bool { return c.Recorded != c.Current }

// Diagnostics returns the findings for the repro file named file:
// repro/non-reproducible for a --real-env recording and
// repro/source-changed listing the changed files (both tool class,
// exit 2), and the repro/version-mismatch warning.
func (c Check) Diagnostics(file string) []diag.Diagnostic {
	var ds []diag.Diagnostic
	if c.Real {
		ds = append(ds, diag.New(diag.ReproNonReproducible,
			file).At(file, 0, 0))
	}
	if len(c.Changed) > 0 {
		ds = append(ds, diag.New(diag.ReproSourceChanged,
			strings.Join(c.Changed, ", ")).At(file, 0, 0))
	}
	if c.VersionMismatch() {
		ds = append(ds, diag.New(diag.ReproVersionMismatch, c.Recorded,
			c.Current).At(file, 0, 0))
	}
	return ds
}

// Invalid is the repro/invalid diagnostic for a repro file that cannot
// be read or decoded.
func Invalid(file string, err error) diag.Diagnostic {
	return diag.New(diag.ReproInvalid, file, err.Error()).At(file, 0, 0)
}

// Verify hashes the files h recorded under root (sources, gohard.yaml
// and the design's inputs) and compares them, and version, with the
// recording. An input that cannot be read is changed. A file that cannot be read for a reason
// other than not existing is an error.
func Verify(fsys env.FS, root string, h Header, version string) (
	Check, error,
) {
	c := Check{Recorded: h.Version, Current: version,
		Real: h.Env != nil && h.Env.Real}
	want := slices.Clone(h.Sources)
	if h.Config != nil {
		want = append(want, *h.Config)
	}
	for _, f := range want {
		data, err := readRegular(fsys, filepath.Join(root,
			filepath.FromSlash(f.Path)))
		switch {
		case errors.Is(err, fs.ErrNotExist), unusable(err):
			c.Changed = append(c.Changed, f.Path)
		case err != nil:
			return Check{}, err
		case Hash(data) != f.SHA256:
			c.Changed = append(c.Changed, f.Path)
		}
	}
	for _, f := range h.Inputs {
		name := filepath.Join(root, filepath.FromSlash(f.Path))
		if f.SHA256 == "" {
			if _, err := fsys.Stat(name); !errors.Is(err,
				fs.ErrNotExist) {
				c.Changed = append(c.Changed, f.Path)
			}
			continue
		}
		data, err := readRegular(fsys, name)
		if err != nil || Hash(data) != f.SHA256 {
			c.Changed = append(c.Changed, f.Path)
		}
	}
	if h.Config == nil {
		cfg, err := HashConfig(fsys, root)
		if unusable(err) {
			cfg, err = &File{Path: ConfigFile}, nil
		}
		if err != nil {
			return Check{}, err
		}
		if cfg != nil {
			c.Changed = append(c.Changed, cfg.Path)
		}
	}
	slices.Sort(c.Changed)
	c.Changed = slices.Compact(c.Changed)
	return c, nil
}

// MaxFileSize is the largest source or gohard.yaml Verify, HashFiles
// and HashConfig read, in bytes: the #20 source limit (10 MB).
const MaxFileSize = 10_000_000

var (
	// ErrNotRegular is returned for a FIFO, device, socket or
	// directory: reading one could block forever or never end.
	ErrNotRegular = errors.New("it is not a regular file")
	// ErrTooLarge is returned for a file over MaxFileSize.
	ErrTooLarge = errors.New("it is larger than 10000000 bytes")
)

// unusable reports whether err says the file is there but not one
// Verify can hash: it counts as changed (exit 2).
func unusable(err error) bool {
	return errors.Is(err, ErrNotRegular) || errors.Is(err, ErrTooLarge)
}

// readRegular reads name only if it is a regular file of at most
// MaxFileSize bytes. A FIFO would block forever and a link to
// /dev/zero never ends, so neither is ever opened.
func readRegular(fsys env.FS, name string) ([]byte, error) {
	fi, err := fsys.Stat(name)
	switch {
	case err != nil:
		return nil, err
	case !fi.Mode().IsRegular():
		return nil, ErrNotRegular
	case fi.Size() > MaxFileSize:
		return nil, ErrTooLarge
	}
	b, err := fsys.ReadFile(name)
	if err == nil && len(b) > MaxFileSize {
		return nil, ErrTooLarge // it grew after Stat
	}
	return b, err
}
