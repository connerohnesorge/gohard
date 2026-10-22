package ide

import (
	"bytes"
	"io"
	"io/fs"
	"slices"
	"sync"

	"github.com/connerohnesorge/gohard/internal/env"
)

// readOnce is the project as one editor run reads it (#18): the reads
// of Base, each answered once and then remembered, so every rebuild
// of the run (a forked server State resuming an older continuation)
// sees the same files even if the disk changed meanwhile. It never
// writes: the run's writes stay in its env.Overlay. It is safe for
// concurrent use.
type readOnce struct {
	base env.FS

	mu    sync.Mutex
	files map[string]readResult
	stats map[string]statResult // key: "s"+name (Stat), "l"+name
	links map[string]readResult
	dirs  map[string]dirResult
}

type (
	readResult struct {
		data []byte
		err  error
	}
	statResult struct {
		fi  fs.FileInfo
		err error
	}
	dirResult struct {
		es  []fs.DirEntry
		err error
	}
)

func newReadOnce(base env.FS) *readOnce {
	return &readOnce{base: base, files: map[string]readResult{},
		stats: map[string]statResult{}, links: map[string]readResult{},
		dirs: map[string]dirResult{}}
}

func denied(op, name string) error {
	return &fs.PathError{Op: op, Path: name, Err: fs.ErrPermission}
}

func (r *readOnce) Getwd() (string, error) { return r.base.Getwd() }

func (r *readOnce) ReadFile(name string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.files[name]
	if !ok {
		res.data, res.err = r.base.ReadFile(name)
		r.files[name] = res
	}
	return bytes.Clone(res.data), res.err
}

func (r *readOnce) stat(kind, name string,
	f func(string) (fs.FileInfo, error),
) (fs.FileInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.stats[kind+name]
	if !ok {
		res.fi, res.err = f(name)
		r.stats[kind+name] = res
	}
	return res.fi, res.err
}

func (r *readOnce) Stat(name string) (fs.FileInfo, error) {
	return r.stat("s", name, r.base.Stat)
}

func (r *readOnce) Lstat(name string) (fs.FileInfo, error) {
	return r.stat("l", name, r.base.Lstat)
}

func (r *readOnce) Readlink(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.links[name]
	if !ok {
		var t string
		t, res.err = r.base.Readlink(name)
		res.data = []byte(t)
		r.links[name] = res
	}
	return string(res.data), res.err
}

func (r *readOnce) ReadDir(name string) ([]fs.DirEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.dirs[name]
	if !ok {
		res.es, res.err = r.base.ReadDir(name)
		r.dirs[name] = res
	}
	return slices.Clone(res.es), res.err
}

func (r *readOnce) Open(name string) (io.ReadCloser, error) {
	b, err := r.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (r *readOnce) WriteFile(name string, _ []byte, _ fs.FileMode) error {
	return denied("write", name)
}

func (r *readOnce) WriteTemp(dir, _ string, _ []byte) (string, error) {
	return "", denied("write", dir)
}

func (r *readOnce) Rename(oldname, _ string) error {
	return denied("rename", oldname)
}

func (r *readOnce) Chmod(name string, _ fs.FileMode) error {
	return denied("chmod", name)
}

func (r *readOnce) Remove(name string) error {
	return denied("remove", name)
}

func (r *readOnce) MkdirAll(name string, _ fs.FileMode) error {
	return denied("mkdir", name)
}

func (r *readOnce) Create(name string) (io.WriteCloser, error) {
	return nil, denied("create", name)
}

func (r *readOnce) Append(name string) (io.WriteCloser, error) {
	return nil, denied("append", name)
}

var _ env.FS = (*readOnce)(nil)
