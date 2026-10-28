package cli

import (
	"path/filepath"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/workspace"
)

func init() { lookup("parse").Run = runParse }

// runParse is `gohard parse [--dump] [--define NAME=VALUE] FILE` (#31).
// It loads gohard.yaml from the project root (standard, psl.comments,
// psl.strict_keywords, defines; --define overrides defines:), parses
// the one VHDL file FILE and prints its syntax diagnostics in the text
// format with the summary line. With --dump the stable tree dump
// (cst.Dump) comes first on stdout, then the diagnostics. The exit code
// is the diagnostics' (#20): 1 on a syntax error such as
// syntax/undefined-condition, 2 on an invalid configuration (a defined
// built-in identifier) or a missing or unreadable FILE.
func runParse(inv *Invocation) Result {
	if len(inv.Args) != 1 {
		return inv.usage("gohard parse takes one FILE")
	}
	l := inv.LoadConfig()
	r := inv.NewReport()
	r.Diagnostics = append(r.Diagnostics, l.Diagnostics...)
	r.Source = sources(l, nil)
	if !l.Valid() {
		return Result{Report: r}
	}
	fsys := inv.Env.FS
	cwd, err := fsys.Getwd()
	if err != nil {
		r.Diagnostics = append(r.Diagnostics,
			workspace.Unreadable(".", err))
		return Result{Report: r}
	}
	arg := inv.Args[0]
	if fi, err := fsys.Stat(arg); err == nil && fi.IsDir() {
		return inv.usage("gohard parse takes a FILE, not the directory " +
			filepath.ToSlash(arg))
	}
	found := workspace.Discover(fsys, workspace.Options{
		Root: l.Root, Dir: cwd, Paths: inv.Args,
	})
	r.Diagnostics = append(r.Diagnostics, found.Diagnostics...)
	r.Files = len(found.Files)
	if hasTool(r.Diagnostics) {
		return Result{Report: r}
	}
	if len(found.Files) == 0 {
		return inv.usage("gohard parse needs a .vhd or .vhdl FILE, not " +
			filepath.ToSlash(arg))
	}
	srcs, ds := workspace.Read(inv.Env.Jobs, inv.Jobs, fsys, found.Files,
		0)
	r.Diagnostics = append(r.Diagnostics, ds...)
	r.Source = sources(l, srcs)
	if len(srcs) != 1 {
		return Result{Report: r} // too large or unreadable
	}
	src := srcs[0]
	res := parser.Parse(src.Text, parser.FromConfig(l.Config, Version))
	r.Diagnostics = append(r.Diagnostics, res.Diagnostics(src.Path)...)
	if !inv.Bool("dump") {
		return Result{Report: r}
	}
	out := cst.AppendDump(nil, res.Tree())
	out = append(out, diag.RenderText(r)...)
	return Result{Stdout: out, Exit: r.Exit()}
}
