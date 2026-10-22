package env

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Overlay is the file door of a simulation run (#18): a sandbox rooted
// at the project root. Reads see the run's own writes first, then the
// project through Base; every write stays in memory, so the run itself
// never touches the world and the shell decides where the writes go
// (gohard check --sim flushes them to .gohard/run/<hash>/ once the run
// ended, see Files). Names are relative to Root (Getwd is Root); an
// absolute name must lie under Root, and a name that leaves it is
// fs.ErrPermission, so a design cannot read or write outside the
// project.
//
// An Overlay belongs to one run: give every run (a race re-run too)
// its own, so no write is ever seen or flushed twice. It is not safe
// for concurrent use.
type Overlay struct {
	// Base reads the project; nil means an empty project.
	Base FS
	// Root is the absolute project root.
	Root string

	files map[string][]byte // root-relative slash path -> content
	dirs  map[string]bool   // directories created by the run
	gone  map[string]bool   // project files the run removed
	reads map[string]string // project path read -> sha256 ("" absent)
	lists map[string]string // project directory listed -> ListingSHA256
	temps int
}

// NewOverlay returns an empty overlay over base rooted at root.
func NewOverlay(base FS, root string) *Overlay {
	return &Overlay{Base: base, Root: root, files: map[string][]byte{},
		dirs: map[string]bool{}, gone: map[string]bool{},
		reads: map[string]string{}, lists: map[string]string{}}
}

// Input is one project file a run read through Base: its
// root-relative slash path and the lowercase hex sha256 of what it
// read, or "" when the file did not exist. The run's inputs are part
// of what a replay must see again (#29).
type Input struct {
	Path   string
	SHA256 string
}

// Inputs returns every project file the run read (or looked for and
// did not find), sorted by path. A file the run wrote itself and then
// read is not an input.
func (o *Overlay) Inputs() []Input {
	out := make([]Input, 0, len(o.reads))
	for _, p := range slices.Sorted(maps.Keys(o.reads)) {
		out = append(out, Input{Path: p, SHA256: o.reads[p]})
	}
	return out
}

// Listings returns every project directory the run listed (ReadDir),
// sorted by path: Path is the root-relative slash path of the
// directory ("." for the root) and SHA256 is ListingSHA256 of the
// project's entries (the run's own changes left out), or "" when the
// project has no such directory. Like Inputs, they are part of what a
// replay must see again: a design's DIR_OPEN listing (#18) depends on
// them.
func (o *Overlay) Listings() []Input {
	out := make([]Input, 0, len(o.lists))
	for _, p := range slices.Sorted(maps.Keys(o.lists)) {
		out = append(out, Input{Path: p, SHA256: o.lists[p]})
	}
	return out
}

// ListingSHA256 is the lowercase hex sha256 of a directory listing:
// one line per entry in the order given (ReadDir sorts by name), the
// name followed by "/" for a directory and "@" for a symbolic link.
// The project root's Reserved entry is not part of its listing.
func ListingSHA256(es []fs.DirEntry) string {
	var b bytes.Buffer
	for _, e := range es {
		b.WriteString(e.Name())
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			b.WriteByte('@')
		case e.IsDir():
			b.WriteByte('/')
		}
		b.WriteByte('\n')
	}
	return hashHex(b.Bytes())
}

// ProjectListing returns what Overlay.Listings records for the
// project directory rel (a root-relative slash path, "." the root) of
// the project at root in fsys: ListingSHA256 of its entries, the root's
// Reserved entry left out, or "" when there is no such directory. A
// replay compares it with the recording (repro.Verify).
func ProjectListing(fsys FS, root, rel string) (string, error) {
	es, err := fsys.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", err
	}
	if rel == "." {
		es = slices.DeleteFunc(slices.Clone(es),
			func(e fs.DirEntry) bool { return e.Name() == Reserved })
	}
	return ListingSHA256(es), nil
}

// Written is one file a run wrote.
type Written struct {
	// Path is relative to the project root, slash-separated.
	Path string
	Data []byte
}

// Files returns every file the run wrote, sorted by path.
func (o *Overlay) Files() []Written {
	out := make([]Written, 0, len(o.files))
	for _, p := range slices.Sorted(maps.Keys(o.files)) {
		out = append(out, Written{Path: p,
			Data: bytes.Clone(o.files[p])})
	}
	return out
}

// Dirs returns every directory the run created, sorted.
func (o *Overlay) Dirs() []string {
	return slices.Sorted(maps.Keys(o.dirs))
}

// Reserved is GoHard's own directory below the project root (traces,
// repro files, earlier run output, caches). It is outside the sandbox:
// a design that could read it would see the output of earlier runs,
// and two runs with the same inputs would differ.
const Reserved = ".gohard"

// rel maps name to a clean root-relative slash path ("." for the
// root) with every symbolic link of the project resolved; a name
// outside the root, or inside Reserved, before or after resolving its
// links, is fs.ErrPermission. Links are resolved through Base, so a
// link in the project can never lead the run outside the sandbox.
func (o *Overlay) rel(op, name string) (string, error) {
	deny := &fs.PathError{Op: op, Path: name, Err: fs.ErrPermission}
	r, ok := o.lexical(Abs(o.Root, name))
	if !ok {
		return "", deny
	}
	r, ok = o.resolve(r)
	if !ok {
		return "", deny
	}
	return r, nil
}

// lexical maps the absolute path abs to a clean root-relative slash
// path; ok is false when it lies outside the root or inside Reserved.
func (o *Overlay) lexical(abs string) (string, bool) {
	r, err := filepath.Rel(filepath.Clean(o.Root), abs)
	if err != nil || r == ".." ||
		strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	r = filepath.ToSlash(r)
	return r, inside(r)
}

// inside reports whether the clean root-relative slash path r is in
// the sandbox: below the root and not inside Reserved.
func inside(r string) bool {
	return r != ".." && !strings.HasPrefix(r, "../") && r != Reserved &&
		!strings.HasPrefix(r, Reserved+"/")
}

// maxLinks bounds the links resolved for one name, like the kernel's
// ELOOP limit: a cycle of links is refused, never followed forever.
const maxLinks = 40

// resolve follows the symbolic links of the project in the clean
// root-relative path r, component by component (a component the run
// created or removed is its own, never a link). ok is false when a
// link leads outside the sandbox or there are too many links.
func (o *Overlay) resolve(r string) (string, bool) {
	if o.Base == nil || r == "." {
		return r, true
	}
	rest := strings.Split(r, "/")
	done, links := ".", 0
	for len(rest) > 0 {
		next := path.Join(done, rest[0])
		rest = rest[1:]
		if !inside(next) {
			return "", false
		}
		if _, mine := o.files[next]; mine || o.dirs[next] ||
			o.gone[next] {
			done = next
			continue
		}
		fi, err := o.Base.Lstat(o.base(next))
		if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			done = next
			continue
		}
		links++
		t, err := o.Base.Readlink(o.base(next))
		if links > maxLinks || err != nil {
			return "", false
		}
		var tr string
		// A rooted target ("/x" on Windows) is absolute too, as Abs
		// treats it.
		if IsRooted(t) {
			var ok bool
			if tr, ok = o.lexical(Abs(o.Root, t)); !ok {
				return "", false
			}
		} else {
			tr = path.Join(done, filepath.ToSlash(t))
		}
		if !inside(tr) {
			return "", false
		}
		if tr != "." {
			rest = append(strings.Split(tr, "/"), rest...)
		}
		done = "."
	}
	return done, true
}

func (o *Overlay) base(r string) string {
	return filepath.Join(o.Root, filepath.FromSlash(r))
}

// Rel returns the clean root-relative slash path ("." for the root)
// name denotes in the sandbox, with the project's symbolic links
// resolved; a name outside the sandbox is fs.ErrPermission.
func (o *Overlay) Rel(name string) (string, error) {
	return o.rel("rel", name)
}

// Getwd implements FS: the project root.
func (o *Overlay) Getwd() (string, error) { return o.Root, nil }

// ReadFile implements FS.
func (o *Overlay) ReadFile(name string) ([]byte, error) {
	r, err := o.rel("read", name)
	if err != nil {
		return nil, err
	}
	if b, ok := o.files[r]; ok {
		return bytes.Clone(b), nil
	}
	if o.gone[r] || o.Base == nil {
		return nil, notExist("read", name)
	}
	b, err := o.Base.ReadFile(o.base(r))
	switch {
	case err == nil:
		o.reads[r] = hashHex(b)
	case errors.Is(err, fs.ErrNotExist):
		o.reads[r] = ""
	}
	return b, err
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func notExist(op, name string) error {
	return &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
}

// WriteFile implements FS: the content is kept in memory.
func (o *Overlay) WriteFile(name string, data []byte, _ fs.FileMode) error {
	r, err := o.rel("write", name)
	if err != nil {
		return err
	}
	if r == "." || o.isDir(r) {
		return &fs.PathError{Op: "write", Path: name,
			Err: errors.New("is a directory")}
	}
	o.files[r] = bytes.Clone(data)
	delete(o.gone, r)
	return nil
}

// WriteTemp implements FS with names numbered in creation order.
func (o *Overlay) WriteTemp(dir, prefix string, data []byte) (string,
	error,
) {
	o.temps++
	name := filepath.Join(dir, prefix+strconv.Itoa(o.temps))
	return name, o.WriteFile(name, data, 0o644)
}

// Rename implements FS.
func (o *Overlay) Rename(oldname, newname string) error {
	a, err1 := o.rel("rename", oldname)
	b, err2 := o.rel("rename", newname)
	if err := errors.Join(err1, err2); err != nil {
		return err
	}
	if a == b {
		_, err := o.Stat(oldname)
		return err
	}
	data, err := o.ReadFile(oldname)
	if err != nil {
		return err
	}
	if err := o.WriteFile(newname, data, 0o644); err != nil {
		return err
	}
	return o.Remove(oldname)
}

// Chmod implements FS: modes are not kept; the file must exist.
func (o *Overlay) Chmod(name string, _ fs.FileMode) error {
	_, err := o.Stat(name)
	return err
}

// Remove implements FS: a file of the run is dropped, a project file
// is hidden from the run (the project is never changed).
func (o *Overlay) Remove(name string) error {
	r, err := o.rel("remove", name)
	if err != nil {
		return err
	}
	if _, ok := o.files[r]; ok {
		delete(o.files, r)
		if o.Base != nil {
			if _, err := o.Base.Stat(o.base(r)); err == nil {
				o.gone[r] = true
			}
		}
		return nil
	}
	if o.dirs[r] {
		if len(o.list(r)) > 0 {
			return &fs.PathError{Op: "remove", Path: name,
				Err: errors.New("directory not empty")}
		}
		delete(o.dirs, r)
		return nil
	}
	fi, err := o.Stat(name)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if r == "." {
			return &fs.PathError{Op: "remove", Path: name,
				Err: fs.ErrPermission}
		}
		if es, err := o.ReadDir(name); err != nil || len(es) > 0 {
			return &fs.PathError{Op: "remove", Path: name,
				Err: errors.New("directory not empty")}
		}
	}
	o.gone[r] = true
	return nil
}

// MkdirAll implements FS.
func (o *Overlay) MkdirAll(name string, _ fs.FileMode) error {
	r, err := o.rel("mkdir", name)
	if err != nil {
		return err
	}
	for d := r; d != "." && d != "/"; d = path.Dir(d) {
		if _, ok := o.files[d]; ok {
			return &fs.PathError{Op: "mkdir", Path: name,
				Err: fs.ErrExist}
		}
		if !o.isDir(d) {
			o.dirs[d] = true
		}
		delete(o.gone, d)
	}
	return nil
}

// isDir reports whether r is a directory of the run or the project.
func (o *Overlay) isDir(r string) bool {
	if r == "." || o.dirs[r] {
		return true
	}
	if o.gone[r] || o.Base == nil {
		return false
	}
	fi, err := o.Base.Stat(o.base(r))
	return err == nil && fi.IsDir()
}

// Stat implements FS.
func (o *Overlay) Stat(name string) (fs.FileInfo, error) {
	r, err := o.rel("stat", name)
	if err != nil {
		return nil, err
	}
	if b, ok := o.files[r]; ok {
		return memInfo{name: path.Base(r), size: int64(len(b))}, nil
	}
	if o.dirs[r] {
		return memInfo{name: path.Base(r), dir: true}, nil
	}
	if o.gone[r] || o.Base == nil {
		if r == "." {
			return memInfo{name: ".", dir: true}, nil
		}
		return nil, notExist("stat", name)
	}
	return o.Base.Stat(o.base(r))
}

// Lstat implements FS. The overlay resolves every link of a name (see
// rel), so the run never sees a link: Lstat is Stat, and Readlink
// fails.
func (o *Overlay) Lstat(name string) (fs.FileInfo, error) {
	r, err := o.rel("lstat", name)
	if err != nil {
		return nil, err
	}
	if _, ok := o.files[r]; ok || o.dirs[r] || o.gone[r] ||
		o.Base == nil {
		return o.Stat(name)
	}
	return o.Base.Lstat(o.base(r))
}

// Readlink implements FS.
func (o *Overlay) Readlink(name string) (string, error) {
	r, err := o.rel("readlink", name)
	if err != nil {
		return "", err
	}
	if _, ok := o.files[r]; ok || o.dirs[r] || o.gone[r] ||
		o.Base == nil {
		return "", &fs.PathError{Op: "readlink", Path: name,
			Err: fs.ErrInvalid}
	}
	return o.Base.Readlink(o.base(r))
}

// list returns the names the run added directly in directory r.
func (o *Overlay) list(r string) map[string]memInfo {
	out := map[string]memInfo{}
	in := func(p string) (string, bool) {
		if r == "." {
			return p, !strings.Contains(p, "/")
		}
		rest, ok := strings.CutPrefix(p, r+"/")
		return rest, ok && rest != "" && !strings.Contains(rest, "/")
	}
	for _, p := range slices.Sorted(maps.Keys(o.files)) {
		if n, ok := in(p); ok {
			out[n] = memInfo{name: n, size: int64(len(o.files[p]))}
		}
	}
	for _, p := range slices.Sorted(maps.Keys(o.dirs)) {
		if n, ok := in(p); ok {
			out[n] = memInfo{name: n, dir: true}
		}
	}
	return out
}

// ReadDir implements FS: the project's entries and the run's, without
// the removed ones, sorted by name.
func (o *Overlay) ReadDir(name string) ([]fs.DirEntry, error) {
	r, err := o.rel("readdir", name)
	if err != nil {
		return nil, err
	}
	if !o.isDir(r) {
		return nil, notExist("readdir", name)
	}
	byName := map[string]fs.DirEntry{}
	if o.Base != nil {
		es, err := o.Base.ReadDir(o.base(r))
		if err != nil && !o.dirs[r] {
			return nil, err
		}
		if r == "." {
			es = slices.DeleteFunc(slices.Clone(es),
				func(e fs.DirEntry) bool { return e.Name() == Reserved })
		}
		switch {
		case err == nil:
			o.lists[r] = ListingSHA256(es)
		case errors.Is(err, fs.ErrNotExist):
			o.lists[r] = ""
		}
		for _, e := range es {
			if !o.gone[path.Join(r, e.Name())] {
				byName[e.Name()] = e
			}
		}
	}
	added := o.list(r)
	for _, n := range slices.Sorted(maps.Keys(added)) {
		byName[n] = fs.FileInfoToDirEntry(added[n])
	}
	out := make([]fs.DirEntry, 0, len(byName))
	for _, n := range slices.Sorted(maps.Keys(byName)) {
		out = append(out, byName[n])
	}
	return out, nil
}

// Open implements FS.
func (o *Overlay) Open(name string) (io.ReadCloser, error) {
	b, err := o.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// Create implements FS: the content is stored when the writer is
// closed.
func (o *Overlay) Create(name string) (io.WriteCloser, error) {
	if _, err := o.rel("create", name); err != nil {
		return nil, err
	}
	return &memWriter{o: o, name: name}, nil
}

// Append implements FS.
func (o *Overlay) Append(name string) (io.WriteCloser, error) {
	w := &memWriter{o: o, name: name}
	b, err := o.ReadFile(name)
	switch {
	case err == nil:
		w.buf.Write(b)
	case errors.Is(err, fs.ErrNotExist):
	default:
		return nil, err
	}
	return w, nil
}

type memWriter struct {
	o    *Overlay
	name string
	buf  bytes.Buffer
}

func (w *memWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

func (w *memWriter) Close() error {
	return w.o.WriteFile(w.name, w.buf.Bytes(), 0o644)
}

// memInfo is the FileInfo of an overlay file or directory. It has no
// modification time: a run's files carry no wall time.
type memInfo struct {
	name string
	size int64
	dir  bool
}

func (m memInfo) Name() string { return m.name }
func (m memInfo) Size() int64  { return m.size }
func (m memInfo) Mode() fs.FileMode {
	if m.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (m memInfo) ModTime() time.Time { return time.Time{} }
func (m memInfo) IsDir() bool        { return m.dir }
func (m memInfo) Sys() any           { return nil }

var _ FS = (*Overlay)(nil)
