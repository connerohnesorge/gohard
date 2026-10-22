package workspace

import (
	"cmp"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
)

// MaxFileSize is the largest file GoHard parses, in bytes (10 MB, #20).
// A larger file is not read and gets the warning workspace/file-too-large.
// The 1,000,000-CST-node limit belongs to the parser (#31).
const MaxFileSize = 10_000_000

// maxHops bounds the symbolic links followed while resolving one path;
// more than this is a loop (the Linux limit is 40 as well).
const maxHops = 40

// Options say where to look.
type Options struct {
	// Root is the absolute project root (see FindRoot). Every reported
	// path is relative to it.
	Root string
	// Dir is the absolute directory relative Paths are resolved against
	// (the current directory of the run).
	Dir string
	// Paths are the command-line paths, files or directories. Empty
	// means the whole project root.
	Paths []string
	// MaxFileSize overrides the size limit when > 0 (for tests).
	MaxFileSize int64
}

// File is one discovered VHDL file.
type File struct {
	// Path is relative to the project root and uses '/' (#20). It is
	// what diagnostics and output show.
	Path string
	// Name is the OS path to read the file through env.FS.
	Name string
	// Size is the size in bytes at discovery time.
	Size int64
	// TooLarge is set when Size is over the limit; the file is not read
	// and Discover reported workspace/file-too-large for it.
	TooLarge bool
}

// Result is the outcome of Discover.
type Result struct {
	// Files are sorted by Path. A file reached through several paths
	// (symbolic links, overlapping arguments) is listed once, under the
	// smallest of its paths.
	Files []File
	// Diagnostics are the discovery problems: cli/missing-path,
	// workspace/symlink-loop and workspace/unreadable (exit 2), and
	// workspace/file-too-large (a warning). cli/no-files is left to the
	// caller, which knows whether --allow-empty was given.
	Diagnostics []diag.Diagnostic
}

// IsVHDL reports whether name has a VHDL extension: .vhd or .vhdl in
// any letter case.
func IsVHDL(name string) bool {
	ext := path.Ext(filepath.ToSlash(name))
	return strings.EqualFold(ext, ".vhd") || strings.EqualFold(ext, ".vhdl")
}

// Discover finds the VHDL files named by o. Directories are walked
// recursively in name order; directories whose name starts with '.'
// (such as .git and .gohard) are skipped during the walk but are walked
// when named on the command line. Symbolic links are followed; a link
// that leads back into a directory being walked, or a chain of more
// than 40 links, is a workspace/symlink-loop. Files named on the command
// line count only when they have a VHDL extension. Missing paths are
// reported and the remaining paths are still searched.
//
// The result depends only on the file tree and on the set of paths: the
// order of o.Paths and repeated paths do not change it.
func Discover(fsys env.FS, o Options) Result {
	w := &walker{
		fsys: fsys, o: o, limit: cmp.Or(o.MaxFileSize, MaxFileSize),
		files: map[string]File{}, visited: map[string]bool{},
	}
	for _, a := range w.args() {
		w.arg(a)
	}
	files := slices.SortedFunc(maps.Values(w.files), func(a, b File) int {
		return cmp.Compare(a.Path, b.Path)
	})
	files = w.sameFiles(files)
	for _, f := range files {
		if f.TooLarge {
			w.diags = append(w.diags, TooLarge(f.Path, f.Size, w.limit))
		}
	}
	return Result{Files: files, Diagnostics: diag.Normalize(w.diags)}
}

// sameFiles drops files that are the same file as one with a smaller
// path although their real paths differ: hard links, and two spellings
// of one name on a case-insensitive file system (src/a.vhd and
// SRC/a.vhd on macOS or Windows). files is sorted by Path, so the first
// of each group is kept. Only files of equal size can be the same, so
// only those are compared.
func (w *walker) sameFiles(files []File) []File {
	bySize := map[int64][]int{}
	for i, f := range files {
		bySize[f.Size] = append(bySize[f.Size], i)
	}
	drop := make([]bool, len(files))
	infos := make([]fs.FileInfo, len(files))
	stat := func(i int) fs.FileInfo {
		if infos[i] == nil {
			fi, err := w.fsys.Stat(files[i].Name)
			if err != nil {
				return nil
			}
			infos[i] = fi
		}
		return infos[i]
	}
	for _, size := range slices.Sorted(maps.Keys(bySize)) {
		group := bySize[size]
		if len(group) < 2 {
			continue
		}
		for a, i := range group {
			if drop[i] {
				continue
			}
			for _, j := range group[a+1:] {
				if drop[j] {
					continue
				}
				fi, fj := stat(i), stat(j)
				if fi != nil && fj != nil && os.SameFile(fi, fj) {
					drop[j] = true // j > i, so its Path is larger
				}
			}
		}
	}
	out := files[:0]
	for i, f := range files {
		if !drop[i] {
			out = append(out, f)
		}
	}
	return out
}

// TooLarge is the workspace/file-too-large warning for a file of size
// bytes over limit.
func TooLarge(p string, size, limit int64) diag.Diagnostic {
	return diag.New(diag.WorkspaceFileTooLarge,
		"size "+strconv.FormatInt(size, 10)+" bytes",
		strconv.FormatInt(limit, 10)+" bytes ("+mb(limit)+")",
	).At(p, 0, 0)
}

func mb(n int64) string {
	if n%1_000_000 == 0 {
		return strconv.FormatInt(n/1_000_000, 10) + " MB"
	}
	return strconv.FormatFloat(float64(n)/1e6, 'f', -1, 64) + " MB"
}

type walker struct {
	fsys    env.FS
	o       Options
	limit   int64
	files   map[string]File // by real path
	visited map[string]bool // real directories already walked
	diags   []diag.Diagnostic
}

// target is one command-line path.
type target struct {
	disp string // root-relative slash path
	name string // absolute, lexically clean OS path
}

// args returns the distinct command-line paths sorted by their display
// path, or the root when there are none.
func (w *walker) args() []target {
	if len(w.o.Paths) == 0 {
		return []target{{disp: ".", name: filepath.Clean(w.o.Root)}}
	}
	var ts []target
	for _, p := range w.o.Paths {
		name := env.Abs(w.o.Dir, filepath.FromSlash(p))
		ts = append(ts, target{disp: w.display(name), name: name})
	}
	slices.SortFunc(ts, func(a, b target) int {
		return cmp.Or(cmp.Compare(a.disp, b.disp),
			cmp.Compare(a.name, b.name))
	})
	return slices.Compact(ts)
}

// display returns name relative to the root with '/' separators.
func (w *walker) display(name string) string {
	rel, err := filepath.Rel(w.o.Root, name)
	if err != nil {
		return filepath.ToSlash(name)
	}
	return filepath.ToSlash(rel)
}

func (w *walker) arg(t target) {
	real, err := realPath(w.fsys, t.name)
	if err == nil {
		var fi fs.FileInfo
		if fi, err = w.fsys.Stat(real); err == nil {
			switch {
			case fi.IsDir():
				w.walk(t.disp, real, nil)
			case fi.Mode().IsRegular() && IsVHDL(t.name):
				w.add(t.disp, real, fi.Size())
			}
			return
		}
	}
	switch {
	case errors.Is(err, errLoop):
		w.report(diag.New(diag.WorkspaceSymlinkLoop, t.disp).
			At(t.disp, 0, 0))
	case errors.Is(err, fs.ErrNotExist):
		w.report(diag.New(diag.CLIMissingPath, t.disp))
	default:
		w.unreadable(t.disp, err)
	}
}

// walk adds the VHDL files under the real directory dir, shown as disp.
// stack holds the real directories being walked above it.
func (w *walker) walk(disp, dir string, stack []string) {
	if slices.Contains(stack, dir) {
		w.report(diag.New(diag.WorkspaceSymlinkLoop, disp).At(disp, 0, 0))
		return
	}
	if w.visited[dir] {
		return // reached again through a link; its files are counted
	}
	w.visited[dir] = true
	entries, err := w.fsys.ReadDir(dir)
	if err != nil {
		w.unreadable(disp, err)
		return
	}
	stack = append(stack, dir)
	for _, e := range entries {
		name := e.Name()
		child := path.Join(disp, name)
		real := filepath.Join(dir, name)
		mode := e.Type()
		size := int64(-1) // unknown until stat
		if mode&fs.ModeSymlink != 0 {
			fi, r, err := w.follow(real)
			switch {
			case errors.Is(err, errLoop):
				w.report(diag.New(diag.WorkspaceSymlinkLoop, child).
					At(child, 0, 0))
				continue
			case errors.Is(err, fs.ErrNotExist):
				if IsVHDL(name) {
					w.unreadable(child, errDangling)
				}
				continue
			case err != nil:
				w.unreadable(child, err)
				continue
			}
			real, mode, size = r, fi.Mode().Type(), fi.Size()
		}
		switch {
		case mode.IsDir():
			if !strings.HasPrefix(name, ".") {
				w.walk(child, real, stack)
			}
		case mode.IsRegular() && IsVHDL(name):
			if size < 0 {
				if size = w.size(child, real); size < 0 {
					continue
				}
			}
			w.add(child, real, size)
		}
	}
}

// follow resolves a symbolic link to its real path and stats it.
func (w *walker) follow(name string) (fs.FileInfo, string, error) {
	real, err := realPath(w.fsys, name)
	if err != nil {
		return nil, "", err
	}
	fi, err := w.fsys.Stat(real)
	return fi, real, err
}

// size stats a regular file found by ReadDir; -1 means it was reported
// unreadable.
func (w *walker) size(disp, real string) int64 {
	fi, err := w.fsys.Stat(real)
	if err != nil {
		w.unreadable(disp, err)
		return -1
	}
	return fi.Size()
}

func (w *walker) add(disp, real string, size int64) {
	if old, ok := w.files[real]; ok && old.Path <= disp {
		return
	}
	w.files[real] = File{
		Path: disp, Name: real, Size: size, TooLarge: size > w.limit,
	}
}

func (w *walker) report(d diag.Diagnostic) { w.diags = append(w.diags, d) }

func (w *walker) unreadable(disp string, err error) {
	w.report(Unreadable(disp, err))
}

// Unreadable is the workspace/unreadable error for path p. The reason
// is the OS error without the absolute path, so output does not depend
// on where the project lives.
func Unreadable(p string, err error) diag.Diagnostic {
	return diag.New(diag.WorkspaceUnreadable, p, Reason(err)).At(p, 0, 0)
}

// Reason returns the message of err without the path that fs.PathError
// and similar wrappers add.
func Reason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

var (
	errLoop     = errors.New("too many levels of symbolic links")
	errDangling = errors.New("broken symbolic link")
)

// realPath resolves every symbolic link in the absolute path name, the
// way the OS does, and returns the resulting absolute path. It fails
// with errLoop after maxHops links.
func realPath(fsys env.FS, name string) (string, error) {
	vol := filepath.VolumeName(name)
	done := vol + string(filepath.Separator)
	todo := split(name[len(vol):])
	hops := 0
	for len(todo) > 0 {
		part := todo[0]
		todo = todo[1:]
		switch part {
		case ".":
			continue
		case "..":
			done = filepath.Dir(done)
			continue
		}
		next := filepath.Join(done, part)
		fi, err := fsys.Lstat(next)
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			done = next
			continue
		}
		if hops++; hops > maxHops {
			return "", &fs.PathError{Op: "resolve", Path: name,
				Err: errLoop}
		}
		link, err := fsys.Readlink(next)
		if err != nil {
			return "", err
		}
		if env.IsRooted(link) {
			v := filepath.VolumeName(link)
			if v == "" {
				v = vol
			}
			done = v + string(filepath.Separator)
			link = link[len(filepath.VolumeName(link)):]
		}
		todo = append(split(link), todo...)
	}
	return done, nil
}

func split(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool {
		return r == '/' || r == filepath.Separator
	})
}
