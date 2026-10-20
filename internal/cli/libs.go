package cli

import (
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/libs"
)

func init() { lookup("libs").Run = runLibs }

// libsHost is the machine `gohard libs detect` and the language
// server's "library not found" quick fix search (#1).
func libsHost(inv *Invocation) libs.Host {
	return libs.HostFrom(inv.Ctx, runtime.GOOS, inv.Env)
}

// runLibs is `gohard libs detect` (#1). It searches the vendor
// installs and GHDL library indexes (internal/libs) and prints the
// gohard.yaml lines for the library sources it finds; libraries it
// cannot read (encrypted or precompiled) are logged as
// libs/unreadable and get no line. --write adds the lines (and the
// encrypted units, under allow_opaque) to gohard.yaml; when a tool has
// several versions it lists them and writes nothing until --version
// picks one (exit 2). Vendor files are only read; stub paths
// (testdata/stubs) are never written.
func runLibs(inv *Invocation) Result {
	if len(inv.Args) > 1 {
		return inv.usage("gohard libs detect takes no arguments")
	}
	l := inv.LoadConfig()
	res := libs.Detect(libsHost(inv), l.Root)
	plan, err := res.Choose(inv.Strings("version"))
	if err != nil {
		return inv.usage(err.Error())
	}
	for _, g := range plan.Groups {
		for _, left := range g.Left {
			if lb := left.Library; lb.Unreadable != "" {
				inv.logDiag(diag.New(diag.LibsUnreadable, lb.Name,
					lb.Folder, lb.Unreadable))
			}
		}
	}
	text := []byte(plan.Text())
	if !inv.Bool("write") {
		return Result{Stdout: text}
	}
	if !l.Valid() {
		return inv.Fail(l.Diagnostics...)
	}
	if len(plan.Ambiguous) > 0 {
		for _, g := range plan.Ambiguous {
			inv.Logf("%s: several versions found; pick one with "+
				"--version %s=VERSION (nothing written)", g[0].Name,
				g[0].Product)
		}
		return Result{Stdout: text, Exit: diag.ExitTool}
	}
	return inv.writeLibs(l, plan, text)
}

// logDiag logs one diagnostic to stderr in the text form.
func (inv *Invocation) logDiag(d diag.Diagnostic) {
	inv.Logf("%s[%s]: %s", d.Severity, d.Code, d.Message)
}

// writeLibs adds the plan's lines that gohard.yaml does not have yet.
func (inv *Invocation) writeLibs(l config.Loaded, plan libs.Plan,
	out []byte,
) Result {
	var lines []string
	n := 0
	for _, g := range plan.Groups {
		var entries []string
		for _, e := range g.Entries {
			if slices.ContainsFunc(l.Config.Libraries,
				func(c config.Library) bool {
					return strings.EqualFold(c.Name, e.Name)
				}) {
				inv.Logf("%s is already mapped in gohard.yaml; left "+
					"as it is", e.Name)
				continue
			}
			entries = append(entries, e.Entry())
		}
		if len(entries) > 0 {
			lines = append(lines, "# "+g.Install.Label()+
				" (gohard libs detect)")
			lines = append(lines, entries...)
			n += len(entries)
		}
	}
	var opaque []string
	for _, u := range plan.Opaque {
		if !slices.ContainsFunc(l.Config.AllowOpaque, func(c string) bool {
			return strings.EqualFold(c, u)
		}) {
			opaque = append(opaque, u)
		}
	}
	if n == 0 && len(opaque) == 0 {
		inv.Logf("nothing to add to gohard.yaml")
		return Result{Stdout: out}
	}
	text := l.Source
	if !l.Present {
		text = nil
	}
	if n > 0 {
		ed, ok := libs.LibrariesEdit(text, lines...)
		if !ok {
			return inv.Fail(newUsage("gohard.yaml writes libraries: as " +
				"a flow mapping; add the lines by hand"))
		}
		text = ed.Apply(text)
	}
	if len(opaque) > 0 {
		ed, ok := libs.OpaqueEdit(text, opaque)
		if !ok {
			return inv.Fail(newUsage("gohard.yaml writes " +
				"allow_opaque: in a form --write does not edit; add " +
				strings.Join(opaque, ", ") + " by hand"))
		}
		text = ed.Apply(text)
	}
	if _, ds := config.Parse(config.FileName, text); hasTool(ds) {
		return inv.Fail(ds...) // never write a config that fails
	}
	target := filepath.Join(l.Root, config.FileName)
	if err := writeOutput(inv.Env.FS, target, text); err != nil {
		return inv.Fail(diag.New(diag.CLIOutputFailed, config.FileName,
			errReason(err)))
	}
	var added []string
	if n > 0 {
		added = append(added, plural(n, "library", "libraries"))
	}
	if len(opaque) > 0 {
		added = append(added, plural(len(opaque), "allow_opaque unit",
			"allow_opaque units"))
	}
	inv.Logf("added %s to gohard.yaml", strings.Join(added, " and "))
	return Result{Stdout: out}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
