package cli

import (
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

func init() { lookup("config").Run = runConfig }

// runConfig is `gohard config check` and `gohard config schema` (#28).
//
// check prints the diagnostics of gohard.yaml (and of --define) in the
// chosen format: exit 0 when valid (warnings print but do not fail), 1 on
// warnings under --strict, 2 when the configuration is invalid. schema
// prints schema/gohard.schema.json.
func runConfig(inv *Invocation) Result {
	sub := inv.Args[0]
	if len(inv.Args) > 1 {
		return inv.usage("gohard config " + sub + " takes no arguments")
	}
	if sub == "schema" {
		return Result{Stdout: config.Schema()}
	}
	l := inv.LoadConfig()
	r := inv.NewReport()
	r.Diagnostics = l.Diagnostics
	r.Source = sources(l, nil)
	if l.Present {
		r.Files = 1
	}
	return Result{Report: r}
}

// LoadConfig loads gohard.yaml from the project root of the current
// directory and, when the command has --define, applies the defines and
// logs each one that overrides a defines: entry to stderr (#20, #28).
// A command must exit 2 with l.Diagnostics when !l.Valid().
func (inv *Invocation) LoadConfig() config.Loaded {
	l := config.Load(inv.Env.FS, "")
	for _, f := range inv.Command.Flags {
		if f.Name != "define" {
			continue
		}
		c, logs, ds := config.ApplyDefines(l.Config,
			inv.Strings("define"))
		for _, line := range logs {
			inv.Logf("%s", line)
		}
		l.Config = c
		l.Diagnostics = diag.Normalize(append(l.Diagnostics, ds...))
	}
	return l
}

// sources gives a report the text of gohard.yaml and of the VHDL files
// the run read, by the paths diagnostics use (relative to the root).
func sources(l config.Loaded,
	srcs []workspace.Source,
) func(string) ([]byte, bool) {
	return func(path string) ([]byte, bool) {
		if path == config.FileName && l.Source != nil {
			return l.Source, true
		}
		i, ok := slices.BinarySearchFunc(srcs, path,
			func(s workspace.Source, p string) int {
				return strings.Compare(s.Path, p)
			})
		if !ok {
			return nil, false
		}
		return srcs[i].Text, true
	}
}
