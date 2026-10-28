package env_test

import (
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
)

// project is a simulated project at /work with two files.
func project(t *testing.T) (env.FS, string) {
	t.Helper()
	root := filepath.FromSlash("/work")
	e := simenv.New(simenv.Options{Dir: root, Files: map[string][]byte{
		"in.txt": []byte("stimulus"), "sub/a.txt": []byte("a")}})
	return e.FS, root
}

// TestOverlayReadsThroughWritesStay: reads see the run's writes, then
// the project; the project itself never changes.
func TestOverlayReadsThroughWritesStay(t *testing.T) {
	base, root := project(t)
	o := env.NewOverlay(base, root)
	if b, err := o.ReadFile("in.txt"); err != nil || string(b) != "stimulus" {
		t.Fatalf("read through: %q %v", b, err)
	}
	if err := o.WriteFile("in.txt", []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := o.ReadFile(filepath.Join(root, "in.txt")); string(b) != "mine" {
		t.Errorf("overlay read %q", b)
	}
	if b, _ := base.ReadFile(
		filepath.Join(root, "in.txt"),
	); string(
		b,
	) != "stimulus" {
		t.Errorf("the project changed: %q", b)
	}
	w, err := o.Create("out/log.txt")
	if err == nil {
		_, _ = io.WriteString(w, "line\n")
		err = w.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	a, err := o.Append("out/log.txt")
	if err == nil {
		_, _ = io.WriteString(a, "more\n")
		err = a.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	got := o.Files()
	want := []env.Written{{Path: "in.txt", Data: []byte("mine")},
		{Path: "out/log.txt", Data: []byte("line\nmore\n")}}
	if len(got) != len(want) {
		t.Fatalf("files %+v", got)
	}
	for i := range want {
		if got[i].Path != want[i].Path ||
			string(got[i].Data) != string(want[i].Data) {
			t.Errorf("file %d = %s %q, want %s %q", i, got[i].Path,
				got[i].Data, want[i].Path, want[i].Data)
		}
	}
}

// TestOverlaySandbox: no name outside the root, nor inside GoHard's
// own .gohard directory, is read, written, listed or stat'ed; names
// that only look alike (.gohardx) stay inside the sandbox.
func TestOverlaySandbox(t *testing.T) {
	root := filepath.FromSlash("/work")
	base := simenv.New(simenv.Options{Dir: root, Files: map[string][]byte{
		"in.txt":                  []byte("stimulus"),
		".gohard/traces/tb.trace": []byte("earlier run"),
		".gohard/run/0/out.txt":   []byte("earlier output"),
		".gohardx/kept.txt":       []byte("mine"),
		"sub/a.txt":               []byte("a"),
	}}).FS
	o := env.NewOverlay(base, root)
	for _, name := range []string{"..", "sub/../..", "../x",
		"sub/../../x", filepath.FromSlash("/etc/passwd"), ".gohard",
		".gohard/traces/tb.trace", "sub/../.gohard/run/0/out.txt",
		filepath.FromSlash("/work/.gohard/traces"), ".gohard/new.txt"} {
		if _, err := o.ReadFile(name); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("read %s: %v", name, err)
		}
		err := o.WriteFile(name, nil, 0o644)
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("write %s: %v", name, err)
		}
		if _, err := o.Stat(name); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("stat %s: %v", name, err)
		}
		if _, err := o.ReadDir(name); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("readdir %s: %v", name, err)
		}
		if err := o.MkdirAll(name, 0o755); !errors.Is(err,
			fs.ErrPermission) {
			t.Errorf("mkdir %s: %v", name, err)
		}
	}
	if len(o.Files()) != 0 || len(o.Dirs()) != 0 {
		t.Errorf("sandboxed writes kept: %+v %v", o.Files(), o.Dirs())
	}
	es, err := o.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	if want := []string{".gohardx", "in.txt", "sub"}; !slices.Equal(
		names, want) {
		t.Errorf("root listing %v, want %v", names, want)
	}
	if b, err := o.ReadFile(".gohardx/kept.txt"); err != nil ||
		string(b) != "mine" {
		t.Errorf(".gohardx read %q %v", b, err)
	}
}

// TestOverlayReadDirSorted: listings merge the project and the run,
// hide removed entries and are sorted, whatever the write order.
func TestOverlayReadDirSorted(t *testing.T) {
	base, root := project(t)
	o := env.NewOverlay(base, root)
	for _, n := range []string{"zz.txt", "b.txt", "sub/c.txt"} {
		if err := o.WriteFile(n, []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.MkdirAll("newdir/deep", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := o.Remove("in.txt"); err != nil {
		t.Fatal(err)
	}
	names := func(dir string) []string {
		es, err := o.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range es {
			out = append(out, e.Name())
		}
		return out
	}
	if got, want := names("."), []string{"b.txt", "newdir", "sub",
		"zz.txt"}; !slices.Equal(got, want) {
		t.Errorf("root listing %q, want %q", got, want)
	}
	if got, want := names(
		"sub",
	), []string{
		"a.txt",
		"c.txt",
	}; !slices.Equal(
		got,
		want,
	) {
		t.Errorf("sub listing %q, want %q", got, want)
	}
	if _, err := o.ReadFile("in.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("removed file still read: %v", err)
	}
	if fi, err := o.Stat("newdir/deep"); err != nil || !fi.IsDir() {
		t.Errorf("created dir: %v %v", fi, err)
	}
	if got := o.Dirs(); !slices.Equal(got, []string{"newdir",
		"newdir/deep"}) {
		t.Errorf("dirs %q", got)
	}
}

// TestOverlayDeterministic: the same writes in any order give the same
// Files (sorted, last write wins), and two overlays never share state.
func TestOverlayDeterministic(t *testing.T) {
	base, root := project(t)
	names := []string{"a", "b/c", "d", "b/a"}
	var ref []env.Written
	for seed := range uint64(20) {
		o := env.NewOverlay(base, root)
		other := env.NewOverlay(base, root)
		r := &env.SplitMix64{State: seed}
		perm := slices.Clone(names)
		for i := len(perm) - 1; i > 0; i-- {
			j := env.Intn(r, i+1)
			perm[i], perm[j] = perm[j], perm[i]
		}
		for _, n := range perm {
			_ = o.WriteFile(n, []byte("old"), 0o644)
		}
		for _, n := range perm {
			_ = o.WriteFile(n, []byte(n), 0o644)
		}
		if len(other.Files()) != 0 {
			t.Fatal("overlays share writes")
		}
		got := o.Files()
		if ref == nil {
			ref = got
			continue
		}
		for i := range ref {
			if got[i].Path != ref[i].Path ||
				string(got[i].Data) != string(ref[i].Data) {
				t.Errorf("seed %d: file %d = %+v, want %+v", seed, i,
					got[i], ref[i])
			}
		}
	}
	if !slices.IsSortedFunc(ref, func(a, b env.Written) int {
		return cmpString(a.Path, b.Path)
	}) {
		t.Errorf("files not sorted: %+v", ref)
	}
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// TestOverlayNoBase: without a project every read misses, writes work.
func TestOverlayNoBase(t *testing.T) {
	o := env.NewOverlay(nil, filepath.FromSlash("/work"))
	if _, err := o.ReadFile("x"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("read: %v", err)
	}
	for i := range 3 {
		name, err := o.WriteTemp(".", "t.", []byte(strconv.Itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := o.Rename(name, "final"); err != nil {
			t.Fatal(err)
		}
	}
	got := o.Files()
	if len(got) != 1 || got[0].Path != "final" || string(got[0].Data) != "2" {
		t.Errorf("files %+v", got)
	}
}

// TestOverlaySymlinks: a link in the project is followed only while
// it stays in the sandbox. A link out of the root or into .gohard is
// fs.ErrPermission for every operation, however it is reached
// (relative, absolute, chained, through a parent directory), and a
// cycle is refused; a link inside the project reads its target.
func TestOverlaySymlinks(t *testing.T) {
	root := filepath.FromSlash("/work")
	sim := simenv.New(simenv.Options{Dir: root, Files: map[string][]byte{
		"data/in.txt":             []byte("stimulus"),
		".gohard/traces/tb.trace": []byte("earlier run"),
		"/outside/secret.txt":     []byte("secret"),
	}})
	base := sim.FS
	links := [][2]string{
		{"/outside", "/work/out"},
		{"/outside/secret.txt", "/work/data/abs.txt"},
		{"../../outside/secret.txt", "/work/data/rel.txt"},
		{".gohard", "/work/gh"},
		{"../.gohard/traces", "/work/data/traces"},
		{"rel.txt", "/work/data/chain.txt"},
		{"in.txt", "/work/data/ok.txt"},
		{"/work/data", "/work/datalink"},
		{"loop2", "/work/loop1"},
		{"loop1", "/work/loop2"},
		{"..", "/work/data/up"},
	}
	for _, l := range links {
		if err := base.Symlink(filepath.FromSlash(l[0]),
			filepath.FromSlash(l[1])); err != nil {
			t.Fatal(l, err)
		}
	}
	o := env.NewOverlay(base, root)
	for _, name := range []string{"out", "out/secret.txt",
		"out/new.txt", "data/abs.txt", "data/rel.txt", "gh",
		"gh/traces/tb.trace", "data/traces/tb.trace",
		"data/chain.txt", "datalink/rel.txt", "loop1", "loop2/x",
		"data/up/gh/traces/tb.trace", "data/up/data/up/out/secret.txt",
		"data/up/.gohard/traces/tb.trace"} {
		if _, err := o.ReadFile(name); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("read %s: %v", name, err)
		}
		if err := o.WriteFile(name, []byte("x"), 0o644); !errors.Is(err,
			fs.ErrPermission) {
			t.Errorf("write %s: %v", name, err)
		}
		if _, err := o.Stat(name); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("stat %s: %v", name, err)
		}
		if _, err := o.Lstat(name); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("lstat %s: %v", name, err)
		}
		if err := o.MkdirAll(name, 0o755); !errors.Is(err,
			fs.ErrPermission) {
			t.Errorf("mkdir %s: %v", name, err)
		}
	}
	if len(o.Files()) != 0 || len(o.Dirs()) != 0 {
		t.Errorf("writes through links kept: %+v %v", o.Files(),
			o.Dirs())
	}
	for _, name := range []string{"data/ok.txt", "datalink/in.txt",
		"datalink/ok.txt", "data/up/data/in.txt"} {
		if b, err := o.ReadFile(name); err != nil ||
			string(b) != "stimulus" {
			t.Errorf("read %s: %q %v", name, b, err)
		}
	}
	// A write through an in-project link lands on the link's target.
	if err := o.WriteFile("datalink/new.txt", []byte("n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if fs := o.Files(); len(fs) != 1 || fs[0].Path != "data/new.txt" {
		t.Errorf("files %+v, want data/new.txt", fs)
	}
}

// TestOverlayInputs: every project file the run read is an input with
// the hash of what it read, and a file it looked for and did not find
// is an absent input; files the run wrote are not inputs, and the
// list is sorted whatever the read order.
func TestOverlayInputs(t *testing.T) {
	base, root := project(t)
	o := env.NewOverlay(base, root)
	for _, n := range []string{"sub/a.txt", "missing.txt", "in.txt",
		"sub/a.txt"} {
		_, _ = o.ReadFile(n)
	}
	if err := o.WriteFile("mine.txt", []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = o.ReadFile("mine.txt")
	got := o.Inputs()
	want := []env.Input{
		{Path: "in.txt", SHA256: "present"},
		{Path: "missing.txt"},
		{Path: "sub/a.txt", SHA256: "ca978112ca1bbdcafac231b39a23dc4d" +
			"a786eff8147c4e72b9807785afee48bb"},
	}
	if len(got) != len(want) {
		t.Fatalf("inputs %+v", got)
	}
	for i := range want {
		if got[i].Path != want[i].Path || (want[i].SHA256 == "") !=
			(got[i].SHA256 == "") {
			t.Errorf("input %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	if got[2].SHA256 != want[2].SHA256 {
		t.Errorf("hash of a: %s", got[2].SHA256)
	}
}
