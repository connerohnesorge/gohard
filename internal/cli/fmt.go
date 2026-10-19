package cli

import (
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/format"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/span"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

func init() { lookup("fmt").Run = runFmt }

// runFmt is `gohard fmt [--check] [PATH...]` (#32). It loads gohard.yaml
// (standard and the format.* keys), discovers the VHDL files of PATHS
// (the project root when none are given), parses each one and formats
// it with internal/format, the code the language server uses.
//
// Without --check each file that changes is rewritten in place: the
// formatted text goes to a temp file in the same directory, which is
// renamed over the original, so a file is never left half-written. A
// failed write is format/write-failed (exit 2) and leaves the original
// untouched.
//
// With --check nothing is written; each file that would change gets
// format/would-change at its first change (exit 1), so the run lists
// them.
//
// Either way a file that does not parse is format/not-parsed (exit 1)
// next to its syntax diagnostics, and is left as it is. The other
// diagnostics of a file that parses (a construct the standard does not
// allow, an undefined condition) are gohard check's to report, so they
// do not fail fmt. An invalid gohard.yaml, a missing path or no VHDL
// files (unless --allow-empty) exit 2 (#20); the files that were found
// are still formatted. --define sets conditional-analysis identifiers,
// which decide the inactive branches left unchanged.
func runFmt(inv *Invocation) Result {
	r := inv.NewReport()
	l := inv.LoadConfig()
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
	found := workspace.Discover(fsys, workspace.Options{
		Root: l.Root, Dir: cwd, Paths: inv.Args,
	})
	r.Diagnostics = append(r.Diagnostics, found.Diagnostics...)
	r.Files = len(found.Files)
	srcs, ds := workspace.Read(inv.Env.Jobs, inv.Jobs, fsys, found.Files,
		0)
	r.Diagnostics = append(r.Diagnostics, ds...)
	// A file over the size limit is never parsed, so it won't parse
	// (#20, #32): format/not-parsed next to workspace/file-too-large.
	r.Diagnostics = append(r.Diagnostics,
		notParsedTooLarge(found.Diagnostics, ds)...)
	r.Source = sources(l, srcs)
	if len(found.Files) == 0 && !hasTool(r.Diagnostics) &&
		!inv.Bool("allow-empty") {
		r.Diagnostics = append(r.Diagnostics,
			diag.New(diag.CLINoFiles, searched(inv.Args)))
	}
	popts := parser.FromConfig(l.Config, Version)
	opts := format.FromConfig(l.Config)
	check := inv.Bool("check")
	// Parsing and formatting are pure, so they run as jobs; the results
	// are merged in file order, and writes happen one by one after.
	outs := env.Map(inv.Env.Jobs, inv.Jobs, srcs,
		func(s workspace.Source) fmtOutcome {
			return formatSource(s, popts, opts)
		})
	for i, o := range outs {
		src := srcs[i]
		r.Diagnostics = append(r.Diagnostics, o.diags...)
		if o.res == nil || !o.res.Changed() {
			continue
		}
		if check {
			r.Diagnostics = append(r.Diagnostics,
				wouldChange(src, o.res.Edits[0]))
			continue
		}
		if err := rewrite(fsys, src.Name, o.res.Text); err != nil {
			r.Diagnostics = append(r.Diagnostics,
				diag.New(diag.FormatWriteFailed, errReason(err)).
					At(src.Path, 0, 0))
		}
	}
	return Result{Report: r}
}

// fmtOutcome is the formatting of one file: its parser diagnostics, and
// the result when the file parses.
type fmtOutcome struct {
	diags []diag.Diagnostic
	res   *format.Result
}

func formatSource(s workspace.Source, popts parser.Options,
	opts format.Options,
) fmtOutcome {
	pr := parser.Parse(s.Text, popts)
	var o fmtOutcome
	switch {
	case pr.TooLarge:
		// The parser reported workspace/file-too-large (a warning,
		// #20); like a file over the byte limit it is not parsed, so
		// it won't parse and is left as it is (#32).
		o.diags = append(pr.Diagnostics(s.Path),
			diag.New(diag.FormatNotParsed).At(s.Path, 0, 0))
	case !format.Parses(pr):
		// The syntax diagnostics say why the file was not formatted.
		o.diags = append(pr.Diagnostics(s.Path),
			diag.New(diag.FormatNotParsed).At(s.Path, 0, 0))
	default:
		res := format.Format(pr, opts)
		o.res = &res
	}
	return o
}

// notParsedTooLarge is format/not-parsed for every file that discovery
// or reading skipped with workspace/file-too-large.
func notParsedTooLarge(lists ...[]diag.Diagnostic) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, ds := range lists {
		for _, d := range ds {
			if d.Code == diag.WorkspaceFileTooLarge {
				out = append(out, diag.New(diag.FormatNotParsed).
					At(d.Path, 0, 0))
			}
		}
	}
	return out
}

// wouldChange is format/would-change at the first edit of the file.
func wouldChange(s workspace.Source, e format.Edit) diag.Diagnostic {
	line, col := span.NewIndex(s.Text).LineCol(e.Start)
	return diag.New(diag.FormatWouldChange).At(s.Path, line, col)
}

// rewrite replaces the file name with text through a temp file in the
// same directory and a rename. A symbolic link is followed, so the file
// it points to is rewritten and the link stays. The temp file gets the
// permission bits of the file it replaces before the rename, so only
// the text changes: a 0600 file stays private, an executable bit and a
// read-only file stay as they were.
func rewrite(fsys env.FS, name string, text []byte) error {
	target, err := linkTarget(fsys, name)
	if err != nil {
		return err
	}
	fi, err := fsys.Stat(target)
	if err != nil {
		return err
	}
	dir, base := filepath.Split(target)
	if dir == "" {
		dir = "."
	}
	tmp, err := fsys.WriteTemp(dir, "."+base+".", text)
	if err != nil {
		return err
	}
	err = fsys.Chmod(tmp, fi.Mode().Perm())
	if err == nil {
		err = fsys.Rename(tmp, target)
	}
	if err != nil {
		fsys.Remove(tmp)
	}
	return err
}

// linkTarget follows the symbolic links of name (the last element
// only, at most 40 of them; discovery already rejected loops) and
// returns the path of the regular file.
func linkTarget(fsys env.FS, name string) (string, error) {
	for range 40 {
		fi, err := fsys.Lstat(name)
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			return name, nil
		}
		dest, err := fsys.Readlink(name)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(name), dest)
		}
		name = dest
	}
	return "", errLinkLoop
}

var errLinkLoop = errors.New("too many levels of symbolic links")
