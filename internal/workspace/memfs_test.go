package workspace

import (
	"errors"
	"io"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"time"
)

// memFS is a tiny in-memory env.FS with directories, files and symbolic
// links, enough for discovery. Paths are absolute OS paths. Unused
// methods fail.
type memFS struct {
	nodes map[string]*memNode
	cwd   string
	// denied names fail every access with fs.ErrPermission.
	denied map[string]bool
	// reads counts ReadFile calls by name.
	reads map[string]int
}

type memNode struct {
	mode fs.FileMode // fs.ModeDir, fs.ModeSymlink or 0
	data []byte
	link string
	size int64 // reported size; defaults to len(data)
}

func newMemFS(root string) *memFS {
	m := &memFS{
		nodes:  map[string]*memNode{},
		cwd:    root,
		denied: map[string]bool{},
		reads:  map[string]int{},
	}
	m.mkdir(root)
	return m
}

func (m *memFS) abs(name string) string {
	if !filepath.IsAbs(name) {
		name = filepath.Join(m.cwd, name)
	}
	return filepath.Clean(name)
}

func (m *memFS) mkdir(name string) {
	name = m.abs(name)
	for d := name; ; d = filepath.Dir(d) {
		if _, ok := m.nodes[d]; !ok {
			m.nodes[d] = &memNode{mode: fs.ModeDir}
		}
		if filepath.Dir(d) == d {
			return
		}
	}
}

func (m *memFS) file(name, data string) {
	name = m.abs(name)
	m.mkdir(filepath.Dir(name))
	m.nodes[name] = &memNode{data: []byte(data), size: int64(len(data))}
}

func (m *memFS) sized(name string, size int64) {
	m.file(name, "")
	m.nodes[m.abs(name)].size = size
}

func (m *memFS) symlink(name, target string) {
	name = m.abs(name)
	m.mkdir(filepath.Dir(name))
	m.nodes[name] = &memNode{mode: fs.ModeSymlink,
		link: filepath.FromSlash(target)}
}

var errMemLoop = errors.New("too many levels of symbolic links")

// resolve follows links in every component (and the last one if
// follow), like the OS.
func (m *memFS) resolve(name string, follow bool) (string, error) {
	name = m.abs(name)
	vol := filepath.VolumeName(name)
	done := vol + string(filepath.Separator)
	todo := split(name[len(vol):])
	hops := 0
	for len(todo) > 0 {
		part := todo[0]
		todo = todo[1:]
		if part == "." {
			continue
		}
		if part == ".." {
			done = filepath.Dir(done)
			continue
		}
		next := filepath.Join(done, part)
		if m.denied[next] {
			return "", fs.ErrPermission
		}
		n, ok := m.nodes[next]
		if !ok {
			return "", fs.ErrNotExist
		}
		if n.mode == fs.ModeSymlink && (len(todo) > 0 || follow) {
			if hops++; hops > 40 {
				return "", errMemLoop
			}
			l := n.link
			if filepath.IsAbs(l) {
				done = vol + string(filepath.Separator)
				l = l[len(filepath.VolumeName(l)):]
			}
			todo = append(split(l), todo...)
			continue
		}
		if len(todo) > 0 && n.mode != fs.ModeDir {
			return "", errors.New("not a directory")
		}
		done = next
	}
	return done, nil
}

func (m *memFS) stat(op, name string, follow bool) (fs.FileInfo, error) {
	p, err := m.resolve(name, follow)
	if err != nil {
		return nil, &fs.PathError{Op: op, Path: name, Err: err}
	}
	return memInfo{filepath.Base(p), m.nodes[p]}, nil
}

func (m *memFS) Getwd() (string, error) { return m.cwd, nil }

func (m *memFS) ReadFile(name string) ([]byte, error) {
	m.reads[m.abs(name)]++
	fi, err := m.stat("open", name, true)
	if err != nil {
		return nil, err
	}
	n := fi.(memInfo).n
	if n.mode == fs.ModeDir {
		return nil, &fs.PathError{Op: "read", Path: name,
			Err: errors.New("is a directory")}
	}
	return slices.Clone(n.data), nil
}

func (m *memFS) Stat(name string) (fs.FileInfo, error) {
	return m.stat("stat", name, true)
}

func (m *memFS) Lstat(name string) (fs.FileInfo, error) {
	return m.stat("lstat", name, false)
}

func (m *memFS) Readlink(name string) (string, error) {
	p, err := m.resolve(name, false)
	if err != nil {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: err}
	}
	if n := m.nodes[p]; n.mode == fs.ModeSymlink {
		return n.link, nil
	}
	return "", &fs.PathError{Op: "readlink", Path: name,
		Err: fs.ErrInvalid}
}

func (m *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	p, err := m.resolve(name, true)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	var out []fs.DirEntry
	for _, k := range slices.Sorted(maps.Keys(m.nodes)) {
		if k != p && filepath.Dir(k) == p {
			out = append(out, fs.FileInfoToDirEntry(
				memInfo{filepath.Base(k), m.nodes[k]}))
		}
	}
	return out, nil
}

var errUnused = errors.New("memFS: not implemented")

func (m *memFS) WriteFile(string, []byte, fs.FileMode) error {
	return errUnused
}

func (m *memFS) WriteTemp(string, string, []byte) (string, error) {
	return "", errUnused
}
func (m *memFS) Rename(string, string) error           { return errUnused }
func (m *memFS) Remove(string) error                   { return errUnused }
func (m *memFS) MkdirAll(string, fs.FileMode) error    { return errUnused }
func (m *memFS) Open(string) (io.ReadCloser, error)    { return nil, errUnused }
func (m *memFS) Create(string) (io.WriteCloser, error) { return nil, errUnused }
func (m *memFS) Append(string) (io.WriteCloser, error) { return nil, errUnused }

type memInfo struct {
	name string
	n    *memNode
}

func (i memInfo) Name() string       { return i.name }
func (i memInfo) Size() int64        { return i.n.size }
func (i memInfo) Mode() fs.FileMode  { return i.n.mode | 0o644 }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return i.n.mode == fs.ModeDir }
func (i memInfo) Sys() any           { return nil }

// rootDir is an absolute directory on any OS.
func rootDir() string {
	if filepath.Separator == '\\' {
		return `C:\w`
	}
	return "/w"
}

// ospath joins slash path p to the root.
func ospath(p string) string {
	return filepath.Join(rootDir(), filepath.FromSlash(p))
}
