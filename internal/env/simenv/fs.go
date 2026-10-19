package simenv

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/connerohnesorge/gohard/internal/env"
)

// Errors of the simulated file system besides the io/fs sentinels.
var (
	// ErrInjected is wrapped by every seeded or scripted fault, so a
	// test can tell an injected failure from a real bug.
	ErrInjected = errors.New("simenv: injected fault")
	// ErrNotDir and ErrIsDir mirror ENOTDIR and EISDIR.
	ErrNotDir = errors.New("not a directory")
	ErrIsDir  = errors.New("is a directory")
	// ErrNotEmpty mirrors ENOTEMPTY.
	ErrNotEmpty = errors.New("directory not empty")
	// ErrLoop mirrors ELOOP: too many symbolic links.
	ErrLoop = errors.New("too many levels of symbolic links")
)

// maxHops is the symbolic-link limit of a lookup, as on Linux.
const maxHops = 40

// Rate is a fault probability in thousandths: 0 never, 1000 (Always)
// on every opportunity.
type Rate uint16

// Always is the rate of a fault that happens every time.
const Always Rate = 1000

// Faults are seeded fault rates (P6). Each opportunity draws from a
// generator keyed by the seed, so the same seed and the same calls fail
// in the same places. A zero rate draws nothing, so turning one fault on
// does not move the others.
type Faults struct {
	// ShortRead: a streaming Read returns fewer bytes than asked.
	ShortRead Rate
	// ReadError: ReadFile, Open or a streaming Read fails.
	ReadError Rate
	// Vanish: an entry listed by ReadDir is deleted right after the
	// listing, so the walk finds it gone.
	Vanish Rate
	// WriteError: WriteFile writes a prefix and fails; WriteTemp,
	// Rename and MkdirAll fail without a change; a streaming Write
	// writes a prefix and fails.
	WriteError Rate
}

// Op names a file operation for scripted faults.
type Op string

// The operations FailNext can target.
const (
	OpRead    Op = "read"    // ReadFile, Open and streaming reads
	OpWrite   Op = "write"   // WriteFile, WriteTemp, streaming writes
	OpRename  Op = "rename"  // Rename (matched on the old name)
	OpRemove  Op = "remove"  // Remove
	OpMkdir   Op = "mkdir"   // MkdirAll
	OpStat    Op = "stat"    // Stat and Lstat
	OpReadDir Op = "readdir" // ReadDir
)

type node struct {
	mode   fs.FileMode // type bits and permissions
	data   []byte
	target string // symbolic links
	mod    time.Time
	kids   map[string]*node // directories
}

func (n *node) isDir() bool { return n.mode.IsDir() }

func (n *node) isLink() bool { return n.mode&fs.ModeSymlink != 0 }

type scripted struct {
	op   Op
	name string
	err  error
}

// FS is an in-memory file system implementing env.FS. Modification
// times come from the virtual clock. It is safe for concurrent use.
type FS struct {
	mu      sync.Mutex
	root    *node
	wd      string
	clock   *Clock
	faults  Faults
	rng     env.SplitMix64
	tmp     uint64
	pending []scripted
}

// NewFS returns an empty file system whose current directory dir
// ("" means DefaultDir) exists. seed and faults drive fault injection.
func NewFS(clock *Clock, dir string, seed uint64, faults Faults) *FS {
	if dir == "" {
		dir = DefaultDir
	}
	f := &FS{
		clock:  clock,
		faults: faults,
		rng:    env.SplitMix64{State: seed ^ faultSalt},
	}
	f.root = f.newNode(fs.ModeDir | 0o755)
	f.wd = "/"
	f.wd = f.abs(dir)
	if err := f.mkdirAll(f.wd, 0o755); err != nil {
		panic("simenv: " + err.Error())
	}
	return f
}

func (f *FS) newNode(mode fs.FileMode) *node {
	n := &node{mode: mode, mod: f.clock.Now()}
	if mode.IsDir() {
		n.kids = map[string]*node{}
	}
	return n
}

// abs cleans name and resolves it against the current directory.
func (f *FS) abs(name string) string {
	name = filepath.ToSlash(name)
	if !path.IsAbs(name) {
		name = path.Join(f.wd, name)
	}
	return path.Clean(name)
}

func parts(p string) []string {
	if p == "/" {
		return nil
	}
	return strings.Split(p[1:], "/")
}

// lookup resolves the absolute clean path p. follow says whether a
// final symbolic link is followed. It returns the node and its real
// path.
func (f *FS) lookup(p string, follow bool) (*node, string, error) {
	for hops := 0; ; hops++ {
		if hops > maxHops {
			return nil, "", ErrLoop
		}
		n, real, next, err := f.step(p, follow)
		if err != nil || next == "" {
			return n, real, err
		}
		p = next
	}
}

// step walks p until the first symbolic link to follow. It returns the
// node when there is none, or the rewritten path to continue with.
func (f *FS) step(p string, follow bool) (*node, string, string, error) {
	ps := parts(p)
	cur, curPath := f.root, "/"
	for i, name := range ps {
		if !cur.isDir() {
			return nil, "", "", ErrNotDir
		}
		kid := cur.kids[name]
		if kid == nil {
			return nil, "", "", fs.ErrNotExist
		}
		last := i == len(ps)-1
		if kid.isLink() && (!last || follow) {
			t := kid.target
			if !path.IsAbs(t) {
				t = path.Join(curPath, t)
			}
			rest := append([]string{t}, ps[i+1:]...)
			return nil, "", path.Clean(path.Join(rest...)), nil
		}
		cur, curPath = kid, path.Join(curPath, name)
	}
	return cur, curPath, "", nil
}

// parent resolves the directory that holds p and returns it with p's
// base name.
func (f *FS) parent(p string) (*node, string, error) {
	if p == "/" {
		return nil, "", fs.ErrInvalid
	}
	dir, err := f.lookupDir(path.Dir(p))
	return dir, path.Base(p), err
}

func (f *FS) lookupDir(p string) (*node, error) {
	n, _, err := f.lookup(p, true)
	if err != nil {
		return nil, err
	}
	if !n.isDir() {
		return nil, ErrNotDir
	}
	return n, nil
}

// draw reports whether a fault of rate r happens now.
func (f *FS) draw(r Rate) bool {
	if r == 0 {
		return false
	}
	return env.Intn(&f.rng, int(Always)) < int(r)
}

// fault returns the scripted or seeded fault for (op, p), if any.
func (f *FS) fault(op Op, p string, r Rate) error {
	for i, s := range f.pending {
		if s.op == op && s.name == p {
			f.pending = slices.Delete(f.pending, i, i+1)
			return s.err
		}
	}
	if f.draw(r) {
		return ErrInjected
	}
	return nil
}

// FailNext makes the next op on name fail with an error wrapping
// ErrInjected and err (err may be nil). Scripted faults are used in the
// order they were added.
func (f *FS) FailNext(op Op, name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := ErrInjected
	if err != nil {
		e = fmt.Errorf("%w: %w", ErrInjected, err)
	}
	f.pending = append(f.pending, scripted{op, f.abs(name), e})
}

func pathErr(op, name string, err error) error {
	return &fs.PathError{Op: op, Path: name, Err: err}
}

// Getwd implements env.FS.
func (f *FS) Getwd() (string, error) { return f.wd, nil }

// ReadFile implements env.FS.
func (f *FS) ReadFile(name string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.abs(name)
	n, err := f.lookupFile(p)
	if err == nil {
		err = f.fault(OpRead, p, f.faults.ReadError)
	}
	if err != nil {
		return nil, pathErr("read", name, err)
	}
	return bytes.Clone(n.data), nil
}

func (f *FS) lookupFile(p string) (*node, error) {
	n, _, err := f.lookup(p, true)
	if err == nil && n.isDir() {
		err = ErrIsDir
	}
	return n, err
}

// WriteFile implements env.FS. An injected write fault leaves a
// truncated prefix of data behind, like a disk that fills up.
func (f *FS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.abs(name)
	n, err := f.openFile(p, perm, true)
	if err != nil {
		return pathErr("open", name, err)
	}
	if err := f.fault(OpWrite, p, f.faults.WriteError); err != nil {
		n.data = bytes.Clone(data[:env.Intn(&f.rng, len(data))])
		return pathErr("write", name, err)
	}
	n.data = bytes.Clone(data)
	return nil
}

// openFile returns the regular file at p, creating it if needed, and
// truncating it if trunc.
func (f *FS) openFile(p string, perm fs.FileMode, trunc bool) (
	*node, error,
) {
	n, _, err := f.lookup(p, true)
	switch {
	case err == nil && n.isDir():
		return nil, ErrIsDir
	case err == nil:
		if trunc {
			n.data = nil
		}
		n.mod = f.clock.Now()
		return n, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	// A dangling final link creates its target, as with open(2).
	if l, real, lerr := f.lookup(p, false); lerr == nil && l.isLink() {
		t := l.target
		if !path.IsAbs(t) {
			t = path.Join(path.Dir(real), t)
		}
		p = path.Clean(t)
	}
	dir, base, err := f.parent(p)
	if err != nil {
		return nil, err
	}
	n = f.newNode(perm.Perm())
	dir.kids[base] = n
	return n, nil
}

// WriteTemp implements env.FS. Temporary names are prefix plus a
// counter plus ".tmp"; the result is absolute.
func (f *FS) WriteTemp(dir, prefix string, data []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.abs(dir)
	parent, err := f.lookupDir(d)
	if err != nil {
		return "", pathErr("createtemp", dir, err)
	}
	var base string
	for {
		f.tmp++
		base = prefix + strconv.FormatUint(f.tmp, 10) + ".tmp"
		if parent.kids[base] == nil {
			break
		}
	}
	p := path.Join(d, base)
	if err := f.fault(OpWrite, p, f.faults.WriteError); err != nil {
		return "", pathErr("write", p, err)
	}
	n := f.newNode(0o600)
	n.data = bytes.Clone(data)
	parent.kids[base] = n
	return p, nil
}

// Rename implements env.FS with the rules of os.Rename on Unix: a file
// replaces a file, nothing replaces an existing directory (fs.ErrExist,
// even a directory renamed onto itself), and a directory cannot move
// below itself.
func (f *FS) Rename(oldname, newname string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, np := f.abs(oldname), f.abs(newname)
	fail := func(err error) error {
		return pathErr("rename", oldname+" "+newname, err)
	}
	odir, obase, err := f.parent(op)
	if err != nil {
		return fail(err)
	}
	ndir, nbase, err := f.parent(np)
	if err != nil {
		return fail(err)
	}
	n := odir.kids[obase]
	if n == nil {
		return fail(fs.ErrNotExist)
	}
	if err := f.fault(OpRename, op, f.faults.WriteError); err != nil {
		return fail(err)
	}
	// Moving a directory into its own subtree is EINVAL on every OS.
	// The destination parent was resolved through symbolic links, so
	// compare nodes, not paths.
	if n.isDir() && (strings.HasPrefix(np, op+"/") || within(ndir, n)) {
		return fail(fs.ErrInvalid)
	}
	if old := ndir.kids[nbase]; old != nil {
		switch {
		case old.isDir():
			return fail(fs.ErrExist)
		case n.isDir():
			return fail(ErrNotDir)
		}
	}
	delete(odir.kids, obase)
	ndir.kids[nbase] = n
	return nil
}

// within reports whether d is dir or a directory below it. Links are
// not followed, so the walk ends.
func within(d, dir *node) bool {
	if d == dir {
		return true
	}
	for _, name := range slices.Sorted(maps.Keys(dir.kids)) {
		if k := dir.kids[name]; k.isDir() && within(d, k) {
			return true
		}
	}
	return false
}

// Remove implements env.FS. It does not follow a final link.
func (f *FS) Remove(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.abs(name)
	dir, base, err := f.parent(p)
	if err == nil && dir.kids[base] == nil {
		err = fs.ErrNotExist
	}
	if err == nil && dir.kids[base].isDir() &&
		len(dir.kids[base].kids) > 0 {
		err = ErrNotEmpty
	}
	if err == nil {
		err = f.fault(OpRemove, p, 0)
	}
	if err != nil {
		return pathErr("remove", name, err)
	}
	delete(dir.kids, base)
	return nil
}

// MkdirAll implements env.FS.
func (f *FS) MkdirAll(name string, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.abs(name)
	if err := f.fault(OpMkdir, p, f.faults.WriteError); err != nil {
		return pathErr("mkdir", name, err)
	}
	if err := f.mkdirAll(p, perm); err != nil {
		return pathErr("mkdir", name, err)
	}
	return nil
}

func (f *FS) mkdirAll(p string, perm fs.FileMode) error {
	cur := "/"
	for _, name := range parts(p) {
		cur = path.Join(cur, name)
		n, _, err := f.lookup(cur, true)
		switch {
		case err == nil && n.isDir():
			continue
		case err == nil:
			return ErrNotDir
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
		dir, base, err := f.parent(cur)
		if err != nil {
			return err
		}
		if dir.kids[base] != nil { // a dangling link
			return fs.ErrExist
		}
		dir.kids[base] = f.newNode(fs.ModeDir | perm.Perm())
	}
	return nil
}

// Symlink creates name as a symbolic link to target. It is not part of
// env.FS; tests use it to build link layouts (#20 symlink loops).
func (f *FS) Symlink(target, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.abs(name)
	dir, base, err := f.parent(p)
	if err == nil && dir.kids[base] != nil {
		err = fs.ErrExist
	}
	if err != nil {
		return pathErr("symlink", name, err)
	}
	n := f.newNode(fs.ModeSymlink | 0o777)
	n.target = filepath.ToSlash(target)
	dir.kids[base] = n
	return nil
}

// Stat implements env.FS.
func (f *FS) Stat(name string) (fs.FileInfo, error) {
	return f.stat("stat", name, true)
}

// Lstat implements env.FS.
func (f *FS) Lstat(name string) (fs.FileInfo, error) {
	return f.stat("lstat", name, false)
}

func (f *FS) stat(op, name string, follow bool) (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.abs(name)
	n, _, err := f.lookup(p, follow)
	if err == nil {
		err = f.fault(OpStat, p, 0)
	}
	if err != nil {
		return nil, pathErr(op, name, err)
	}
	return info(path.Base(p), n), nil
}

// Readlink implements env.FS.
func (f *FS) Readlink(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _, err := f.lookup(f.abs(name), false)
	if err == nil && !n.isLink() {
		err = fs.ErrInvalid
	}
	if err != nil {
		return "", pathErr("readlink", name, err)
	}
	return n.target, nil
}

// ReadDir implements env.FS. With the Vanish fault, a listed entry may
// be deleted right after the listing.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.abs(name)
	dir, err := f.lookupDir(p)
	if err == nil {
		err = f.fault(OpReadDir, p, f.faults.ReadError)
	}
	if err != nil {
		return nil, pathErr("readdir", name, err)
	}
	names := sortedKeys(dir.kids)
	out := make([]fs.DirEntry, len(names))
	for i, base := range names {
		out[i] = fs.FileInfoToDirEntry(info(base, dir.kids[base]))
		if f.draw(f.faults.Vanish) {
			delete(dir.kids, base)
		}
	}
	return out, nil
}

// Open implements env.FS. The reader sees the content at open time.
func (f *FS) Open(name string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.abs(name)
	n, err := f.lookupFile(p)
	if err == nil {
		err = f.fault(OpRead, p, f.faults.ReadError)
	}
	if err != nil {
		return nil, pathErr("open", name, err)
	}
	return &reader{f: f, name: name, path: p, data: n.data}, nil
}

// Create implements env.FS.
func (f *FS) Create(name string) (io.WriteCloser, error) {
	return f.openWriter(name, true)
}

// Append implements env.FS.
func (f *FS) Append(name string) (io.WriteCloser, error) {
	return f.openWriter(name, false)
}

func (f *FS) openWriter(name string, trunc bool) (io.WriteCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.abs(name)
	n, err := f.openFile(p, 0o666, trunc)
	if err != nil {
		return nil, pathErr("open", name, err)
	}
	return &writer{f: f, name: name, path: p, n: n}, nil
}

// put creates name with data and any missing parents (for Options).
func (f *FS) put(name string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.abs(name)
	if err := f.mkdirAll(path.Dir(p), 0o755); err != nil {
		return pathErr("mkdir", name, err)
	}
	n, err := f.openFile(p, 0o644, true)
	if err != nil {
		return pathErr("open", name, err)
	}
	n.data = bytes.Clone(data)
	return nil
}

// mkdir is MkdirAll without fault injection.
func (f *FS) mkdir(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.mkdirAll(p, 0o755); err != nil {
		return pathErr("mkdir", p, err)
	}
	return nil
}

// Load copies the tree of src into the file system below dst (relative
// to the current directory if not absolute), creating dst. Pass
// os.DirFS(dir) to load a real directory into a test. Symbolic links
// are copied as links when src supports fs.ReadLinkFS. Loading never
// injects faults.
func (f *FS) Load(dst string, src fs.FS) error {
	return fs.WalkDir(src, ".", func(name string, d fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		to := path.Join(f.abs(dst), name)
		switch {
		case d.IsDir():
			return f.mkdir(to)
		case d.Type()&fs.ModeSymlink != 0:
			t, err := fs.ReadLink(src, name)
			if err != nil {
				return err
			}
			return f.Symlink(t, to)
		}
		data, err := fs.ReadFile(src, name)
		if err != nil {
			return err
		}
		return f.put(to, data)
	})
}

// Dump lists the whole tree, one line per entry in path order: the
// path, the mode, then the size and a quoted content for files or the
// target for links. Two runs behaved the same on the file system when
// their dumps are equal.
func (f *FS) Dump() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b strings.Builder
	var walk func(p string, n *node)
	walk = func(p string, n *node) {
		fmt.Fprintf(&b, "%s %v", p, n.mode)
		switch {
		case n.isDir():
		case n.isLink():
			fmt.Fprintf(&b, " -> %s", n.target)
		default:
			fmt.Fprintf(&b, " %d %q", len(n.data), n.data)
		}
		b.WriteByte('\n')
		for _, k := range sortedKeys(n.kids) {
			walk(path.Join(p, k), n.kids[k])
		}
	}
	walk("/", f.root)
	return b.String()
}

type fileInfo struct {
	name string
	size int64
	mode fs.FileMode
	mod  time.Time
}

func info(name string, n *node) fs.FileInfo {
	size := int64(len(n.data))
	if n.isLink() {
		size = int64(len(n.target))
	}
	return fileInfo{name: name, size: size, mode: n.mode, mod: n.mod}
}

func (i fileInfo) Name() string       { return i.name }
func (i fileInfo) Size() int64        { return i.size }
func (i fileInfo) Mode() fs.FileMode  { return i.mode }
func (i fileInfo) ModTime() time.Time { return i.mod }
func (i fileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fileInfo) Sys() any           { return nil }

type reader struct {
	f      *FS
	name   string
	path   string
	data   []byte
	closed bool
}

func (r *reader) Read(p []byte) (int, error) {
	f := r.f
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.closed {
		return 0, pathErr("read", r.name, fs.ErrClosed)
	}
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if err := f.fault(OpRead, r.path, f.faults.ReadError); err != nil {
		return 0, pathErr("read", r.name, err)
	}
	want := min(len(p), len(r.data))
	if want > 1 && f.draw(f.faults.ShortRead) {
		want = 1 + env.Intn(&f.rng, want-1)
	}
	n := copy(p, r.data[:want])
	r.data = r.data[n:]
	return n, nil
}

func (r *reader) Close() error {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	if r.closed {
		return pathErr("close", r.name, fs.ErrClosed)
	}
	r.closed = true
	return nil
}

// writer appends to its node; writes are visible at once.
type writer struct {
	f      *FS
	name   string
	path   string
	n      *node
	closed bool
}

func (w *writer) Write(p []byte) (int, error) {
	f := w.f
	f.mu.Lock()
	defer f.mu.Unlock()
	if w.closed {
		return 0, pathErr("write", w.name, fs.ErrClosed)
	}
	if err := f.fault(OpWrite, w.path, f.faults.WriteError); err != nil {
		k := env.Intn(&f.rng, len(p))
		w.n.data = append(w.n.data, p[:k]...)
		return k, pathErr("write", w.name, err)
	}
	w.n.data = append(w.n.data, p...)
	w.n.mod = f.clock.Now()
	return len(p), nil
}

func (w *writer) Close() error {
	w.f.mu.Lock()
	defer w.f.mu.Unlock()
	if w.closed {
		return pathErr("close", w.name, fs.ErrClosed)
	}
	w.closed = true
	return nil
}
