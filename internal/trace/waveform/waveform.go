// Package waveform is the "Open waveform" action (#36): export a native
// trace to FST on demand and open it in Surfer if `surfer` is on PATH,
// else in GTKWave if `gtkwave` is, else show the FST file's path. There
// is no configuration key for the viewer. Lookup and launch go through
// the env.Executor door; the export is written through env.FS with a
// temporary file and a rename. The language server's code action and
// `gohard lsp` run it (#26).
package waveform

import (
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
	"github.com/connerohnesorge/gohard/v2/internal/trace/fst"
)

// Viewers are the waveform viewers in order of preference.
var Viewers = []string{"surfer", "gtkwave"}

// Result is the outcome of Open.
type Result struct {
	// FST is the exported file: the trace's name with .fst for .trace.
	FST string
	// Viewer is the viewer that was started ("surfer" or "gtkwave"), or
	// "" when none is on PATH; Path is its executable.
	Viewer string
	Path   string
}

// Message is the text to show the user.
func (r Result) Message() string {
	if r.Viewer == "" {
		return "waveform written to " + r.FST +
			" (no surfer or gtkwave on PATH)"
	}
	return "opened " + r.FST + " in " + r.Viewer
}

// FSTName returns the export name of the trace file name.
func FSTName(name string) string {
	return strings.TrimSuffix(name, trace.Ext) + ".fst"
}

// Open exports the trace file name (an OS path) to FST next to it and
// starts the first viewer of Viewers found on PATH. A viewer that is
// found but fails to start is skipped. When none starts, the result has
// no Viewer and the caller shows the FST path.
func Open(fsys env.FS, x env.Executor, name string) (Result, error) {
	t, err := trace.Read(fsys, name)
	if err != nil {
		return Result{}, err
	}
	data, err := fst.Exporter{}.Export(t)
	if err != nil {
		return Result{}, err
	}
	r := Result{FST: FSTName(name)}
	if err := trace.WriteFile(fsys, r.FST, data); err != nil {
		return Result{}, err
	}
	for _, v := range Viewers {
		p, err := x.LookPath(v)
		if err != nil {
			continue
		}
		if err := x.Start(env.Command{Path: p,
			Args: []string{r.FST}}); err != nil {
			continue
		}
		r.Viewer, r.Path = v, p
		break
	}
	return r, nil
}
