package trace

import (
	"path/filepath"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/repro"
)

// Ext is the native trace file extension.
const Ext = ".trace"

// FileName is the trace of a run, relative to the project root:
// `.gohard/traces/<tb>-<seed>.trace`, next to the run's
// `<tb>-<seed>.repro.json` and escaped the same way (#29, #36). The
// folder is not configurable.
func FileName(tb string, seed uint64) string {
	return strings.TrimSuffix(repro.FileName(tb, seed), ".repro.json") +
		Ext
}

// Write stores t under root at FileName(t.Header.Testbench,
// t.Header.Seed), creating the folder, through a temporary file and a
// rename so a reader never sees half a trace. It returns the slash name
// relative to root.
func Write(fsys env.FS, root string, t *Trace) (string, error) {
	name := FileName(t.Header.Testbench, t.Header.Seed)
	err := WriteFile(fsys, filepath.Join(root, filepath.FromSlash(name)),
		Encode(t))
	if err != nil {
		return "", err
	}
	return name, nil
}

// WriteFile writes data to the OS path name through a temporary file in
// the same folder and a rename, creating the folder first.
func WriteFile(fsys env.FS, name string, data []byte) error {
	dir := filepath.Dir(name)
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := fsys.WriteTemp(dir, "."+filepath.Base(name)+".", data)
	if err != nil {
		return err
	}
	if err := fsys.Rename(tmp, name); err != nil {
		fsys.Remove(tmp)
		return err
	}
	return nil
}

// Read reads and decodes the trace file name (an OS path).
func Read(fsys env.FS, name string) (*Trace, error) {
	data, err := fsys.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return Decode(data)
}
