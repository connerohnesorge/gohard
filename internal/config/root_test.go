package config

import (
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
)

// memFS is an in-memory env.FS for root discovery and loading tests.
// Paths are absolute OS paths. A file whose content is nil exists but
// cannot be read (permission denied).
type memFS struct {
	wd    string
	files map[string][]byte
	dirs  map[string]bool
	// dangling holds symbolic links whose target is missing: Lstat
	// finds them, Stat and ReadFile do not.
	dangling map[string]bool
	// modes marks files that are not regular (a FIFO, a device).
	modes map[string]fs.FileMode
}

var _ env.FS = (*memFS)(nil)

func newMemFS(wd string, files map[string]string, dirs ...string) *memFS {
	m := &memFS{wd: abs(wd), files: map[string][]byte{},
		dirs: map[string]bool{}}
	for name, data := range files {
		var b []byte
		if data != "\x00unreadable" {
			b = []byte(data)
		}
		m.files[abs(name)] = b
		m.addDirs(filepath.Dir(abs(name)))
	}
	for _, d := range append(dirs, wd) {
		m.addDirs(abs(d))
	}
	return m
}

// abs turns a slash path such as "/a/b" into an absolute OS path.
func abs(p string) string {
	return filepath.Join(testRoot, filepath.FromSlash(p))
}

// testRoot is an absolute directory on every OS.
var testRoot = func() string {
	r, _ := filepath.Abs(string(filepath.Separator))
	return r
}()

func (m *memFS) addDirs(d string) {
	for {
		m.dirs[d] = true
		p := filepath.Dir(d)
		if p == d {
			return
		}
		d = p
	}
}

func (m *memFS) path(name string) string {
	if !filepath.IsAbs(name) {
		name = filepath.Join(m.wd, name)
	}
	return filepath.Clean(name)
}

func (m *memFS) Getwd() (string, error) { return m.wd, nil }

func (m *memFS) ReadFile(name string) ([]byte, error) {
	p := m.path(name)
	b, ok := m.files[p]
	switch {
	case !ok:
		return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	case b == nil:
		return nil, &fs.PathError{Op: "open", Path: p,
			Err: fs.ErrPermission}
	}
	return b, nil
}

type fileInfo struct {
	name string
	dir  bool
	size int64
	mode fs.FileMode
}

func (f fileInfo) Name() string { return f.name }
func (f fileInfo) Size() int64  { return f.size }
func (f fileInfo) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir | 0o755
	}
	return f.mode | 0o644
}
func (f fileInfo) ModTime() time.Time { return time.Time{} }
func (f fileInfo) IsDir() bool        { return f.dir }
func (f fileInfo) Sys() any           { return nil }

func (m *memFS) Stat(name string) (fs.FileInfo, error) {
	p := m.path(name)
	if b, ok := m.files[p]; ok {
		return fileInfo{name: filepath.Base(p), size: int64(len(b)),
			mode: m.modes[p]}, nil
	}
	if m.dirs[p] {
		return fileInfo{name: filepath.Base(p), dir: true}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
}

func (m *memFS) Lstat(name string) (fs.FileInfo, error) {
	if p := m.path(name); m.dangling[p] {
		return fileInfo{name: filepath.Base(p)}, nil
	}
	return m.Stat(name)
}

func (m *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	p := m.path(name)
	if !m.dirs[p] {
		return nil, &fs.PathError{Op: "readdir", Path: p,
			Err: fs.ErrNotExist}
	}
	return nil, nil
}

var errReadOnly = errors.New("memFS is read-only")

func (m *memFS) WriteFile(string, []byte, fs.FileMode) error {
	return errReadOnly
}
func (m *memFS) WriteTemp(string, string, []byte) (string, error) {
	return "", errReadOnly
}
func (m *memFS) Rename(string, string) error        { return errReadOnly }
func (m *memFS) Remove(string) error                { return errReadOnly }
func (m *memFS) Chmod(string, fs.FileMode) error    { return errReadOnly }
func (m *memFS) MkdirAll(string, fs.FileMode) error { return errReadOnly }
func (m *memFS) Readlink(string) (string, error)    { return "", errReadOnly }
func (m *memFS) Open(string) (io.ReadCloser, error) { return nil, errReadOnly }
func (m *memFS) Create(string) (io.WriteCloser, error) {
	return nil, errReadOnly
}
func (m *memFS) Append(string) (io.WriteCloser, error) {
	return nil, errReadOnly
}

func TestFindRoot(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		dirs  []string
		start string
		root  string
		found Found
	}{
		{"gohard.yaml in start", map[string]string{
			"/p/gohard.yaml": ""}, nil, "/p", "/p", FoundConfig},
		{"gohard.yaml in an ancestor", map[string]string{
			"/p/gohard.yaml": ""}, []string{"/p/a/b/c"}, "/p/a/b/c",
			"/p", FoundConfig},
		{"nearest gohard.yaml wins", map[string]string{
			"/p/gohard.yaml": "", "/p/a/gohard.yaml": ""},
			[]string{"/p/a/b"}, "/p/a/b", "/p/a", FoundConfig},
		{"gohard.yaml beats a nearer .git", map[string]string{
			"/p/gohard.yaml": ""}, []string{"/p/repo/.git",
			"/p/repo/src"}, "/p/repo/src", "/p", FoundConfig},
		{".git directory fallback", nil, []string{"/r/.git", "/r/x/y"},
			"/r/x/y", "/r", FoundGit},
		{".git file fallback (worktree)", map[string]string{
			"/r/.git": "gitdir: elsewhere"}, []string{"/r/x"}, "/r/x",
			"/r", FoundGit},
		{"nearest .git wins", nil, []string{"/r/.git", "/r/sub/.git",
			"/r/sub/x"}, "/r/sub/x", "/r/sub", FoundGit},
		{"current directory or workspace folder fallback", nil,
			[]string{"/w/folder"}, "/w/folder", "/w/folder",
			FoundStart},
		{"unreadable gohard.yaml still marks the root",
			map[string]string{"/p/gohard.yaml": "\x00unreadable"},
			[]string{"/p/a"}, "/p/a", "/p", FoundConfig},
		{"a directory called gohard.yaml counts", nil,
			[]string{"/p/gohard.yaml", "/p/a"}, "/p/a", "/p",
			FoundConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMemFS(tt.start, tt.files, tt.dirs...)
			root, found := FindRoot(m, abs(tt.start))
			if root != abs(tt.root) || found != tt.found {
				t.Errorf("FindRoot = %s, %v; want %s, %v", root, found,
					abs(tt.root), tt.found)
			}
		})
	}
}

// TestFindRootProperty checks FindRoot against a reference model on
// generated trees: the result is an ancestor of start, it holds what
// Found says, and no nearer ancestor holds anything that would win.
func TestFindRootProperty(t *testing.T) {
	rng := env.SplitMix64{State: 1}
	for iter := range 500 {
		depth := 1 + env.Intn(&rng, 6)
		parts := []string{}
		for i := range depth {
			parts = append(parts, string(rune('a'+i)))
		}
		start := "/" + strings.Join(parts, "/")
		var ancestors []string
		for i := depth; i >= 0; i-- {
			ancestors = append(ancestors, "/"+strings.Join(parts[:i], "/"))
		}
		files := map[string]string{}
		var dirs []string
		var wantCfg, wantGit string
		for _, a := range ancestors {
			if env.Intn(&rng, 5) == 0 {
				files[filepath.ToSlash(filepath.Join(a, FileName))] = ""
				if wantCfg == "" {
					wantCfg = a
				}
			}
			if env.Intn(&rng, 4) == 0 {
				dirs = append(dirs, filepath.ToSlash(filepath.Join(a,
					".git")))
				if wantGit == "" {
					wantGit = a
				}
			}
		}
		want, wantFound := start, FoundStart
		switch {
		case wantCfg != "":
			want, wantFound = wantCfg, FoundConfig
		case wantGit != "":
			want, wantFound = wantGit, FoundGit
		}
		m := newMemFS(start, files, dirs...)
		root, found := FindRoot(m, abs(start))
		if root != abs(want) || found != wantFound {
			t.Fatalf("iter %d: files %v dirs %v: FindRoot(%s) = %s, %v;"+
				" want %s, %v", iter, files, dirs, start, root, found,
				want, wantFound)
		}
	}
}

func TestLoad(t *testing.T) {
	t.Run("missing gohard.yaml gives defaults and no diagnostic",
		func(t *testing.T) {
			m := newMemFS("/w/x", nil, "/w/.git")
			l := Load(m, "")
			if len(l.Diagnostics) != 0 || l.Present || !l.Valid() {
				t.Fatalf("Load = %+v", l)
			}
			if l.Root != abs("/w") || l.Found != FoundGit {
				t.Errorf("root %s found %v", l.Root, l.Found)
			}
			if !equalConfig(l.Config, Defaults()) {
				t.Errorf("config is not Defaults: %+v", l.Config)
			}
		})
	t.Run("relative start resolves against Getwd", func(t *testing.T) {
		m := newMemFS("/w", map[string]string{
			"/w/sub/gohard.yaml": "lenient: true\n"})
		l := Load(m, "sub")
		if l.Root != abs("/w/sub") || !l.Config.Lenient {
			t.Errorf("Load = %+v", l)
		}
	})
	t.Run("found in an ancestor", func(t *testing.T) {
		m := newMemFS("/p/a/b", map[string]string{
			"/p/gohard.yaml": "standard: \"1993\"\n"})
		l := Load(m, "")
		if !l.Present || l.Root != abs("/p") ||
			l.Config.Standard != "1993" || len(l.Diagnostics) != 0 {
			t.Errorf("Load = %+v", l)
		}
	})
	cases := []struct {
		name  string
		files map[string]string
		dirs  []string
		want  string
	}{
		{"config/unreadable", map[string]string{
			"/p/gohard.yaml": "\x00unreadable"}, nil,
			"gohard.yaml:1:1: error[config/unreadable]: cannot read " +
				"gohard.yaml: permission denied\n"},
		{"config/unreadable directory", nil,
			[]string{"/p/gohard.yaml"},
			"gohard.yaml:1:1: error[config/unreadable]: cannot read " +
				"gohard.yaml: it is a directory\n"},
		{"library/folder-missing", map[string]string{
			// A VHDL file is an entry (#2); another file is not.
			"/p/gohard.yaml": "libraries:\n" +
				"  lib: [rtl, gone, f.txt, f.vhd]\n" +
				"  other: [/abs/missing]\n",
			"/p/f.txt": "", "/p/f.vhd": ""}, []string{"/p/rtl"},
			"gohard.yaml:2:14: error[library/folder-missing]: library " +
				"lib: folder gone is missing or unreadable\n" +
				"gohard.yaml:2:20: error[library/folder-missing]: " +
				"library lib: folder f.txt is missing or unreadable\n" +
				"gohard.yaml:3:11: error[library/folder-missing]: " +
				"library other: folder /abs/missing is missing or " +
				"unreadable\n"},
	}
	t.Run("dangling gohard.yaml link is unreadable, not skipped",
		func(t *testing.T) {
			// F2 regression: an ancestor's gohard.yaml must not win
			// over a nearer dangling link.
			m := newMemFS("/r/in/deep", map[string]string{
				"/r/gohard.yaml": "lenient: yes\n"}, "/r/.git")
			m.dangling = map[string]bool{abs("/r/in/gohard.yaml"): true}
			root, found := FindRoot(m, abs("/r/in/deep"))
			if root != abs("/r/in") || found != FoundConfig {
				t.Fatalf("FindRoot = %s, %v", root, found)
			}
			l := Load(m, "")
			if l.Valid() || len(l.Diagnostics) != 1 ||
				l.Diagnostics[0].Code != diag.ConfigUnreadable {
				t.Errorf("Load = %+v", l)
			}
		})
	t.Run("a FIFO, a device or a huge file is unreadable, not read",
		func(t *testing.T) {
			// A FIFO would hang Load, /dev/zero would exhaust memory.
			for _, tc := range []struct {
				mode fs.FileMode
				size int
				want string
			}{
				{fs.ModeNamedPipe, 0, "it is not a regular file"},
				{fs.ModeDevice | fs.ModeCharDevice, 0,
					"it is not a regular file"},
				{0, MaxSize + 1, "it is larger than 10000000 bytes"},
			} {
				m := newMemFS("/p", map[string]string{
					"/p/gohard.yaml": strings.Repeat("#", tc.size)})
				m.modes = map[string]fs.FileMode{
					abs("/p/gohard.yaml"): tc.mode}
				l := Load(m, "")
				if len(l.Diagnostics) != 1 ||
					l.Diagnostics[0].Code != diag.ConfigUnreadable ||
					!strings.HasSuffix(l.Diagnostics[0].Message,
						tc.want) {
					t.Errorf("%v: %+v", tc.mode, l.Diagnostics)
				}
			}
			m := newMemFS("/p", map[string]string{
				"/p/gohard.yaml": strings.Repeat("#", MaxSize)})
			if l := Load(m, ""); len(l.Diagnostics) != 0 {
				t.Errorf("a file at the limit: %+v", l.Diagnostics)
			}
		})
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m := newMemFS("/p", tt.files, tt.dirs...)
			l := Load(m, "")
			if l.Valid() {
				t.Errorf("Valid() = true")
			}
			r := &diag.Report{Diagnostics: l.Diagnostics}
			got := string(diag.RenderText(r))
			got = got[:strings.LastIndex(got, "summary:")]
			if filepath.Separator == '/' || !strings.Contains(tt.want,
				"/abs") {
				if got != tt.want {
					t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
				}
			}
			if r.Exit() != diag.ExitTool {
				t.Errorf("exit %d, want 2", r.Exit())
			}
		})
	}
}

// equalConfig compares every key of two configurations.
func equalConfig(a, b Config) bool {
	for _, k := range keys {
		x, y := k.acc.get(&a), k.acc.get(&b)
		if show(x) != show(y) {
			return false
		}
	}
	return true
}

func TestFoundString(t *testing.T) {
	var got []string
	for _, f := range []Found{FoundConfig, FoundGit, FoundStart, 0} {
		got = append(got, f.String())
	}
	want := []string{"gohard.yaml", ".git", "start", "unknown"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
}
