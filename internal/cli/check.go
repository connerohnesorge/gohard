package cli

import (
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

func init() { lookup("check").Run = runCheck }

// runCheck is `gohard check [PATH...]` (#20). It finds the project
// root, loads gohard.yaml, discovers the VHDL files and reads them. The
// parser (#31), analysis (#34) and simulation (#35) stages plug in
// after the files are read; until they exist a run with readable files
// is clean.
func runCheck(inv *Invocation) Result {
	r := inv.NewReport()
	fsys := inv.Env.FS
	if !validSeed(inv.String("seed")) {
		r.Diagnostics = append(r.Diagnostics, newUsage("--seed must "+
			"be an unsigned integer or random, not "+
			strconv.Quote(inv.String("seed"))))
	}
	// One loader for every command (#28): the project root, gohard.yaml,
	// the library folders and --define all go through config.Load and
	// config.ApplyDefines, so check and config check agree.
	l := inv.LoadConfig()
	r.Diagnostics = append(r.Diagnostics, l.Diagnostics...)
	r.Source = sources(l, nil)
	if hasTool(r.Diagnostics) {
		return Result{Report: r} // an invalid gohard.yaml exits 2
	}
	cwd, err := fsys.Getwd()
	if err != nil {
		r.Diagnostics = append(r.Diagnostics,
			workspace.Unreadable(".", err))
		return Result{Report: r}
	}
	_ = l.Config // read by the analysis stages (#31, #34, #35)

	found := workspace.Discover(fsys, workspace.Options{
		Root: l.Root, Dir: cwd, Paths: inv.Args,
	})
	r.Diagnostics = append(r.Diagnostics, found.Diagnostics...)
	r.Files = len(found.Files)
	srcs, ds := workspace.Read(inv.Env.Jobs, inv.Jobs, fsys, found.Files,
		0)
	r.Diagnostics = append(r.Diagnostics, ds...)
	r.Source = sources(l, srcs)
	if len(found.Files) == 0 && !hasTool(r.Diagnostics) &&
		!inv.Bool("allow-empty") {
		r.Diagnostics = append(r.Diagnostics,
			diag.New(diag.CLINoFiles, searched(inv.Args)))
	}
	_ = srcs // parsed by #31

	if simRequested(inv) {
		// --sim, --tb, --seed and --repro are parsed and validated
		// here; the simulation stage (#35, #29) is not built yet, and
		// a clean exit would claim testbenches passed.
		r.Diagnostics = append(r.Diagnostics,
			diag.New(diag.CLINotImplemented, "check --sim"))
	}
	return Result{Report: r}
}

// searched names what discovery looked at, for cli/no-files.
func searched(args []string) string {
	if len(args) == 0 {
		return "the project root"
	}
	return strings.Join(args, ", ")
}

func simRequested(inv *Invocation) bool {
	for _, f := range []string{"sim", "tb", "seed", "repro"} {
		if inv.IsSet(f) {
			return true
		}
	}
	return false
}

func hasTool(ds []diag.Diagnostic) bool {
	for _, d := range ds {
		if d.Class() == diag.Tool {
			return true
		}
	}
	return false
}

func validSeed(s string) bool {
	if s == "" || s == "random" {
		return true
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}
