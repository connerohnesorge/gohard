package cli

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/stdlib"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

func init() { lookup("check").Run = runCheck }

// runCheck is `gohard check [PATH...]` (#20). It finds the project
// root, loads gohard.yaml, discovers the VHDL files and reads them,
// then parses and analyzes them (#31, #34: sema.Analyze, pure jobs on
// the env job runner) and elaborates the instance hierarchy for the
// per-instance static checks (internal/elab/hier). With --sim, --tb,
// --seed or --repro it then runs the testbenches (simulate.go, #35,
// #29).
func runCheck(inv *Invocation) Result {
	r := inv.NewReport()
	fsys := inv.Env.FS
	rp := loadRepro(inv)
	if rp.fail != nil {
		return *rp.fail
	}
	if !validSeed(inv.String("seed")) {
		r.Diagnostics = append(r.Diagnostics, newUsage("--seed must "+
			"be an unsigned integer or random, not "+
			strconv.Quote(inv.String("seed"))))
	}
	// One loader for every command (#28): the project root, gohard.yaml,
	// the library folders and --define all go through config.Load and
	// config.ApplyDefines, so check and config check agree.
	l := inv.LoadConfig()
	ds, stop := rp.apply(inv, &l)
	if stop {
		return inv.Fail(ds...)
	}
	r.Diagnostics = append(r.Diagnostics, ds...)
	r.Diagnostics = append(r.Diagnostics, l.Diagnostics...)
	r.Source = sources(l, nil)
	// The bundled std/ieee files must match their integrity hashes
	// (#33): a mismatch is stdlib/integrity, exit 2. An error inside
	// the bundled text is stdlib/internal-error, also exit 2.
	r.Diagnostics = append(r.Diagnostics, stdlib.Verify(inv.Ctx)...)
	r.Diagnostics = append(r.Diagnostics,
		stdlib.InternalErrors(inv.Ctx)...)
	if hasTool(r.Diagnostics) {
		return Result{Report: r} // an invalid gohard.yaml exits 2
	}
	cwd, err := fsys.Getwd()
	if err != nil {
		r.Diagnostics = append(r.Diagnostics,
			workspace.Unreadable(".", err))
		return Result{Report: r}
	}
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
	ctx := contextSources(inv, l.Root, cwd, found.Files)
	cfg, libs := libraryFolders(inv, l.Root, l.Config, found.Files, ctx)
	res := analyzeSources(inv, r, cfg, srcs, append(ctx, libs...),
		l.Root)

	if simRequested(inv) {
		simulate(inv, r, simInput{root: l.Root, cfg: cfg, res: res,
			files: srcs, repro: rp})
	}
	return Result{Report: r}
}

// contextSources returns the other project files when the command line
// names paths: the named files are checked, but their names resolve
// against the whole project (a package in another file is not a
// missing unit). Files that cannot be read are left out silently;
// only the named files' problems are reported.
func contextSources(inv *Invocation, root, cwd string,
	named []workspace.File,
) []workspace.Source {
	if len(inv.Args) == 0 {
		return nil
	}
	all := workspace.Discover(inv.Env.FS, workspace.Options{
		Root: root, Dir: cwd,
	})
	var extra []workspace.File
	for _, f := range all.Files {
		if !f.TooLarge && !slices.ContainsFunc(named,
			func(n workspace.File) bool { return n.Path == f.Path }) {
			extra = append(extra, f)
		}
	}
	srcs, _ := workspace.Read(inv.Env.Jobs, inv.Jobs, inv.Env.FS, extra,
		0)
	return srcs
}

// libraryFolders returns the configuration with every library folder
// written as a root-relative slash path (so sema maps a file to its
// library by the path diagnostics use) and the sources of the library
// folders outside the project root (vendor libraries such as unisim,
// #1, #28). Those are context only: their units resolve names but their
// own problems are not reported, as they are not part of the project.
// Folders that cannot be read were already reported by config.Load
// (library/folder-missing); files under the root are discovered with
// the project.
func libraryFolders(inv *Invocation, root string, cfg config.Config,
	found []workspace.File, ctx []workspace.Source,
) (config.Config, []workspace.Source) {
	have := map[string]bool{}
	for _, f := range found {
		have[f.Path] = true
	}
	for _, s := range ctx {
		have[s.Path] = true
	}
	var outside []string
	libs := slices.Clone(cfg.Libraries)
	for i, lib := range libs {
		lib.Folders = slices.Clone(lib.Folders)
		for j, f := range lib.Folders {
			abs, rel := rootRelative(root, f.Path)
			lib.Folders[j].Path = rel
			if rel == ".." || strings.HasPrefix(rel, "../") ||
				env.IsRooted(filepath.FromSlash(rel)) {
				outside = append(outside, abs)
			}
		}
		libs[i] = lib
	}
	cfg.Libraries = libs
	if len(outside) == 0 {
		return cfg, nil
	}
	res := workspace.Discover(inv.Env.FS, workspace.Options{
		Root: root, Dir: root, Paths: outside,
	})
	var extra []workspace.File
	for _, f := range res.Files {
		if !f.TooLarge && !have[f.Path] {
			extra = append(extra, f)
		}
	}
	srcs, _ := workspace.Read(inv.Env.Jobs, inv.Jobs, inv.Env.FS, extra,
		0)
	return cfg, srcs
}

// rootRelative returns a gohard.yaml folder (relative to the root or
// absolute) as an absolute OS path and as a clean root-relative slash
// path, the form of the paths workspace reports; a folder on another
// volume stays absolute.
func rootRelative(root, folder string) (abs, rel string) {
	abs = env.Abs(root, filepath.FromSlash(strings.ReplaceAll(folder,
		`\`, "/")))
	r, err := filepath.Rel(root, abs)
	if err != nil {
		return abs, filepath.ToSlash(abs)
	}
	return abs, filepath.ToSlash(r)
}

// analyzeSources parses and analyzes the read sources together with
// the context sources (#31, #34) and elaborates their hierarchy for the
// per-instance checks, adding to r the diagnostics of the checked
// sources and the incomplete entries. The result depends only on the
// sources and the configuration, never on -p.
func analyzeSources(inv *Invocation, r *diag.Report, cfg config.Config,
	srcs, ctx []workspace.Source, root string,
) *sema.Result {
	if len(srcs) == 0 {
		return nil
	}
	in := sema.Input{Config: cfg, ToolVersion: Version,
		Jobs: inv.Env.Jobs, Limit: inv.Jobs,
		Cache: inv.openCache(root, cfg)}
	checked := map[string]bool{}
	for _, s := range srcs {
		checked[s.Path] = true
		in.Files = append(in.Files, sema.Source{Path: s.Path,
			Text: s.Text})
	}
	for _, s := range ctx {
		in.Files = append(in.Files, sema.Source{Path: s.Path,
			Text: s.Text})
	}
	keep := func(ds []diag.Diagnostic) {
		for _, d := range ds {
			if d.Path == "" || checked[d.Path] {
				r.Diagnostics = append(r.Diagnostics, d)
			}
		}
	}
	res := sema.Analyze(in)
	keep(res.Diagnostics)
	r.Incomplete = append(r.Incomplete, res.Incomplete...)
	h := hier.Build(res, hier.Options{})
	keep(h.Diagnostics)
	r.Incomplete = append(r.Incomplete, h.Incomplete...)
	return res
}

// searched names what discovery looked at, for cli/no-files.
func searched(args []string) string {
	if len(args) == 0 {
		return "the project root"
	}
	return strings.Join(args, ", ")
}

func simRequested(inv *Invocation) bool {
	for _, f := range []string{"sim", "tb", "seed", "repro", "env",
		"real-env"} {
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
