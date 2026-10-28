package cli

import (
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/importer"
	"github.com/connerohnesorge/gohard/v2/internal/workspace"
)

func init() { lookup("import").Run = runImport }

// runImport is `gohard import FILE [CORE...]` (#2, #64). It reads a
// ModelSim .do script, Vivado .xpr project, Quartus .qsf file, FuseSoC
// .core file or OSVVM .pro script (internal/importer) and writes
// gohard.yaml at the project root of the current directory: each
// library with the VHDL files the project compiles into it, and the
// newest VHDL standard the project names. Paths in a .xpr, .qsf, .core
// or .pro are relative to the folder of the file that names them (a
// .pro reads the scripts it includes, a .core the .core files its
// depend: entries resolve to: the CORE arguments and every .core under
// the project root). A .do script is read as vsim runs it: paths are
// relative to the folder it runs from (--dir, default the current
// folder), and vcom wildcards are expanded. gohard.yaml gets the paths
// relative to the root. A project file or script of more than 10 MB
// (workspace.MaxFileSize) is workspace/unreadable, and another .core
// file that large provides no core. Diagnostics go to stdout in the
// --format chosen, located in the project file or the file they
// concern.
// Nothing is written when a source file is missing
// (import/missing-file), a file cannot be read (import/syntax), it
// lists no VHDL file (import/no-files) or the root already has a
// gohard.yaml (import/config-exists); all of them exit 2.
func runImport(inv *Invocation) Result {
	if len(inv.Args) == 0 {
		return inv.usage("gohard import takes one project FILE")
	}
	arg := inv.Args[0]
	f, ok := importer.FormatOf(arg)
	if !ok {
		return inv.usage("gohard import reads " + importer.Extensions() +
			" files, not " + filepath.ToSlash(arg))
	}
	for _, a := range inv.Args[1:] {
		if g, _ := importer.FormatOf(a); f.Ext != ".core" ||
			g.Ext != ".core" {
			return inv.usage("gohard import takes one project FILE; " +
				"only a .core FILE takes more .core files, to " +
				"resolve its dependencies")
		}
	}
	if inv.IsSet("dir") && !f.Script {
		return inv.usage("--dir applies to .do scripts; paths in a " +
			f.Name + " are relative to its folder")
	}
	fsys := inv.Env.FS
	cwd, err := fsys.Getwd()
	if err != nil {
		return inv.Fail(workspace.Unreadable(".", err))
	}
	root, found := config.FindRoot(fsys, cwd)
	abs := env.Abs(cwd, filepath.FromSlash(arg))
	name := relSlash(root, abs)
	src, err := readCapped(fsys, abs)
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
	dir := filepath.Dir(abs)
	files := &importFiles{fsys: fsys, root: root, dir: dir, self: abs,
		read: map[string][]byte{}}
	for _, a := range inv.Args[1:] {
		a = env.Abs(cwd, filepath.FromSlash(a))
		if _, err := fsys.Stat(a); err != nil {
			return inv.Fail(diag.New(diag.CLIMissingPath,
				relSlash(root, a)))
		}
		files.given = append(files.given, a)
	}
	r := inv.NewReport()
	r.Source = func(p string) ([]byte, bool) {
		if p == name {
			return src, true
		}
		b, ok := files.read[p]
		return b, ok
	}
	p := f.Parse(name, src, files)
	r.Diagnostics = p.Diagnostics
	if hasTool(r.Diagnostics) {
		return Result{Report: r}
	}
	if f.Script {
		dir = env.Abs(cwd, filepath.FromSlash(inv.String("dir")))
	}
	var entries []importer.Entry
	for _, e := range p.Entries {
		files := []string{env.Abs(dir, filepath.FromSlash(e.Path))}
		if f.Script && hasWildcard(e.Path) {
			// A pattern that matches no file is reported missing.
			if m := glob(fsys, dir, e.Path); len(m) > 0 {
				files = m
			}
		}
		for _, a := range files {
			e.Path = relSlash(root, a)
			if fi, err := fsys.Stat(a); err != nil ||
				!fi.Mode().IsRegular() {
				r.Diagnostics = append(r.Diagnostics, diag.New(
					diag.ImportMissingFile, e.Path).At(e.File, e.Line,
					e.Col))
				continue
			}
			entries = append(entries, e)
		}
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

// hasWildcard reports a *, ? or [ in the slash path p.
func hasWildcard(p string) bool { return strings.ContainsAny(p, "*?[") }

// glob returns the names that the slash pattern pat matches, relative
// to dir, in name order, as vcom expands the wildcards of its file
// arguments (path.Match: *, ? and [...] in any element). dir itself is
// never read as a pattern. ParseDo already skipped a pattern without a
// .vhd or .vhdl extension, so every match is a VHDL file name.
func glob(fsys env.FS, dir, pat string) []string {
	parent, base := path.Split(pat)
	dirs := []string{env.Abs(dir, filepath.FromSlash(parent+"."))}
	if hasWildcard(parent) {
		dirs = glob(fsys, dir, strings.TrimSuffix(parent, "/"))
	}
	var out []string
	for _, d := range dirs {
		es, err := fsys.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range es {
			if ok, _ := path.Match(base, e.Name()); ok {
				out = append(out, filepath.Join(d, e.Name()))
			}
		}
	}
	return out
}

// importFiles reads the files a project file names besides itself
// through env.FS (importer.Files): paths relative to dir, the folder
// of the project file self. It keeps what it read, by root-relative
// path, for the report's Source.
type importFiles struct {
	fsys            env.FS
	root, dir, self string
	given           []string // the CORE arguments, absolute
	read            map[string][]byte
	cores           []string
	walked          bool
}

func (f *importFiles) abs(p string) string {
	return env.Abs(f.dir, filepath.FromSlash(p))
}

// errTooLarge is why gohard import does not read a file of more than
// workspace.MaxFileSize bytes, the largest file GoHard parses (P7): it
// reads the project file, and the scripts and .core files it names,
// whole.
var errTooLarge = errors.New("it is larger than " +
	strconv.Itoa(workspace.MaxFileSize) + " bytes")

// readCapped reads the file a, or fails with errTooLarge.
func readCapped(fsys env.FS, a string) ([]byte, error) {
	if fi, err := fsys.Stat(a); err == nil &&
		fi.Size() > workspace.MaxFileSize {
		return nil, errTooLarge
	}
	b, err := fsys.ReadFile(a)
	if err == nil && len(b) > workspace.MaxFileSize {
		return nil, errTooLarge // it grew after Stat
	}
	return b, err
}

// Read reads the regular file at p, up to workspace.MaxFileSize bytes:
// a larger .core file provides no core, and including a larger script
// is workspace/unreadable.
func (f *importFiles) Read(p string) ([]byte, error) {
	a := f.abs(p)
	fi, err := f.fsys.Stat(a)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fs.ErrNotExist
	}
	b, err := readCapped(f.fsys, a)
	if err == nil {
		f.read[relSlash(f.root, a)] = b
	}
	return b, err
}

// Folder reports a folder at p, and its name.
func (f *importFiles) Folder(p string) (string, bool) {
	a := f.abs(p)
	fi, err := f.fsys.Stat(a)
	return filepath.Base(a), err == nil && fi.IsDir()
}

// Cores lists the CORE arguments, then every .core file under the
// project root in name order (folders whose name starts with '.' and
// symbolic links are not entered), without the project file; each
// path relative to the project file's folder. The root is walked on
// the first call only, so a core without depend: walks nothing.
func (f *importFiles) Cores() []string {
	if f.walked {
		return f.cores
	}
	f.walked = true
	seen := map[string]bool{f.self: true}
	add := func(a string) {
		if seen[a] {
			return
		}
		seen[a] = true
		p, err := filepath.Rel(f.dir, a)
		if err != nil {
			p = a
		}
		f.cores = append(f.cores, filepath.ToSlash(p))
	}
	for _, a := range f.given {
		add(a)
	}
	var walk func(dir string)
	walk = func(dir string) {
		es, err := f.fsys.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range es {
			a := filepath.Join(dir, e.Name())
			switch {
			case e.IsDir() && !strings.HasPrefix(e.Name(), "."):
				walk(a)
			case e.Type().IsRegular() && strings.EqualFold(
				filepath.Ext(e.Name()), ".core"):
				add(a)
			}
		}
	}
	walk(f.root)
	return f.cores
}
