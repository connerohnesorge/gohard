package cli

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/trace"
	"github.com/connerohnesorge/gohard/internal/trace/fst"
	"github.com/connerohnesorge/gohard/internal/trace/ghw"
	"github.com/connerohnesorge/gohard/internal/trace/vcd"
)

func init() { lookup("trace").Run = runTrace }

// exporters are the formats of `gohard trace export --format` (#36,
// GHW #60).
var exporters = []trace.Exporter{
	vcd.Exporter{}, fst.Exporter{}, ghw.Exporter{},
}

// runTrace is `gohard trace export --format vcd|fst|ghw TRACE [-o OUT]`
// (#36, #60): it converts a native trace and writes the export to
// standard output, or with -o/--out to OUT through a temp file and a
// rename. A missing --format is cli/usage and an unknown one
// trace/unknown-format, both exit 2; a missing trace is
// cli/missing-path and an unreadable or invalid one, or one the format
// cannot hold, trace/invalid.
func runTrace(inv *Invocation) Result {
	if len(inv.Args) != 2 {
		return inv.usage("gohard trace export takes one TRACE file")
	}
	if !inv.IsSet("format") {
		return inv.usage(
			"gohard trace export needs --format vcd, fst or ghw")
	}
	format := inv.String("format")
	var x trace.Exporter
	for _, e := range exporters {
		if e.Name() == format {
			x = e
		}
	}
	if x == nil {
		return inv.Fail(diag.New(diag.TraceUnknownFormat, format))
	}
	arg := inv.Args[1]
	shown := filepath.ToSlash(arg)
	data, err := inv.Env.FS.ReadFile(arg)
	if errors.Is(err, fs.ErrNotExist) {
		return inv.Fail(diag.New(diag.CLIMissingPath, shown))
	}
	if err != nil {
		return inv.Fail(diag.New(diag.TraceInvalid, shown, errReason(err)))
	}
	t, err := trace.Decode(data)
	if err != nil {
		return inv.Fail(diag.New(diag.TraceInvalid, shown,
			strings.TrimPrefix(err.Error(), "trace: ")))
	}
	out, err := x.Export(t)
	if err != nil {
		return inv.Fail(diag.New(diag.TraceInvalid, shown, err.Error()))
	}
	if !inv.IsSet("out") {
		return Result{Stdout: out}
	}
	target := inv.String("out")
	if err := writeOutput(inv.Env.FS, target, out); err != nil {
		return inv.Fail(diag.New(diag.CLIOutputFailed,
			filepath.ToSlash(target), errReason(err)))
	}
	return Result{}
}
