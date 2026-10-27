package cli

import (
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/importer"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

func init() { lookup("import").Run = runImport }

// runImport is `gohard import FILE` (#2). It reads a ModelSim .do
// script, Vivado .xpr project or Quartus .qsf file (internal/importer)
// and writes gohard.yaml at the project root of the current directory:
// each library with the VHDL files the project compiles into it, and
// the newest VHDL standard the project names. Paths in the project file
// are relative to its folder; gohard.yaml gets them relative to the
// root. Diagnostics go to stdout in the --format chosen, located in the
// project file. Nothing is written when a source file is missing
// (import/missing-file), the file cannot be read (import/syntax), it
// lists no VHDL file (import/no-files) or the root already has a
// gohard.yaml (import/config-exists); all of them exit 2.
func runImport(inv *Invocation) Result {
	if len(inv.Args) != 1 {
		return inv.usage("gohard import takes one project FILE")
	}
	arg := inv.Args[0]
	f, ok := importer.FormatOf(arg)
	if !ok {
		return inv.usage("gohard import reads " + importer.Extensions() +
			" files, not " + filepath.ToSlash(arg))
	}
	fsys := inv.Env.FS
	cwd, err := fsys.Getwd()
	if err != nil {
		return inv.Fail(workspace.Unreadable(".", err))
	}
	root, found := config.FindRoot(fsys, cwd)
	abs := env.Abs(cwd, filepath.FromSlash(arg))
	name := relSlash(root, abs)
	src, err := fsys.ReadFile(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return inv.Fail(diag.New(diag.CLIMissingPath, name))
	case err != nil:
		if fi, serr := fsys.Stat(abs); serr == nil && fi.IsDir() {
			return inv.usage("gohard import takes a FILE, not the " +
				"directory " + filepath.ToSlash(arg))
		}
		return inv.Fail(workspace.Unreadable(name, err))
	}
	if found == config.FoundConfig {
		return inv.Fail(diag.New(diag.ImportConfigExists,
			config.FileName).At(config.FileName, 1, 1))
	}
	r := inv.NewReport()
	r.Source = func(p string) ([]byte, bool) { return src, p == name }
	p := f.Parse(name, src)
	r.Diagnostics = p.Diagnostics
	if hasTool(r.Diagnostics) {
		return Result{Report: r}
	}
	dir := filepath.Dir(abs)
	var entries []importer.Entry
	for _, e := range p.Entries {
		a := env.Abs(dir, filepath.FromSlash(e.Path))
		e.Path = relSlash(root, a)
		if fi, err := fsys.Stat(a); err != nil || !fi.Mode().IsRegular() {
			r.Diagnostics = append(r.Diagnostics, diag.New(
				diag.ImportMissingFile, e.Path).At(name, e.Line, e.Col))
			continue
		}
		entries = append(entries, e)
	}
	if hasTool(r.Diagnostics) {
		return Result{Report: r}
	}
	cfg, ds := importer.Plan(name, entries)
	r.Diagnostics = append(r.Diagnostics, ds...)
	r.Files = cfg.Files
	if cfg.Files == 0 {
		r.Diagnostics = append(r.Diagnostics, diag.New(
			diag.ImportNoFiles, name).At(name, 1, 1))
		return Result{Report: r}
	}
	text := importer.Render(name, cfg)
	if _, ds := config.Parse(config.FileName, text); hasTool(ds) {
		// Never write a config that fails. FuzzImport checks that
		// Render's output always loads without a diagnostic.
		r.Diagnostics = append(r.Diagnostics, ds...)
		return Result{Report: r}
	}
	target := filepath.Join(root, config.FileName)
	if err := writeOutput(fsys, target, text); err != nil {
		r.Diagnostics = append(r.Diagnostics, diag.New(
			diag.CLIOutputFailed, config.FileName, errReason(err)))
		return Result{Report: r}
	}
	inv.Logf("wrote %s: %s in %s", config.FileName,
		plural(cfg.Files, "file", "files"),
		plural(len(cfg.Libraries), "library", "libraries"))
	return Result{Report: r}
}

// relSlash returns the absolute path abs relative to root with '/'
// separators, or abs itself on another volume.
func relSlash(root, abs string) string {
	r, err := filepath.Rel(root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(r)
}
