package env_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
)

// listingProject is a project at /work with nested directories,
// GoHard's own .gohard and a link.
func listingProject(t *testing.T) (*simenv.FS, string) {
	t.Helper()
	root := filepath.FromSlash("/work")
	e := simenv.New(simenv.Options{Dir: root, Files: map[string][]byte{
		"b.txt": []byte("b"), "a/x.txt": []byte("x"),
		"a/y/z.txt": []byte("z"), ".gohard/run/old/f": []byte("old"),
	}})
	if err := e.FS.Symlink("a", filepath.FromSlash("/work/l")); err != nil {
		t.Fatal(err)
	}
	return e.FS, root
}

// TestOverlayListings: every project directory a run lists is
// recorded once, sorted, with the hash of the project's entries (not
// the run's own, not .gohard); a missing directory is recorded as
// absent; the hash is ListingSHA256 of the project's ReadDir.
func TestOverlayListings(t *testing.T) {
	base, root := listingProject(t)
	o := env.NewOverlay(base, root)
	if err := o.MkdirAll("a/new", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := o.WriteFile("mine.txt", []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"a", ".", "a", "a/new"} {
		if _, err := o.ReadDir(d); err != nil {
			t.Fatal(d, err)
		}
	}
	got := o.Listings()
	paths := make([]string, len(got))
	for i, l := range got {
		paths[i] = l.Path
	}
	if want := []string{".", "a", "a/new"}; !slices.Equal(paths, want) {
		t.Fatalf("listings %v, want %v", paths, want)
	}
	es, err := base.ReadDir(filepath.FromSlash("/work/a"))
	if err != nil {
		t.Fatal(err)
	}
	if got[1].SHA256 != env.ListingSHA256(es) {
		t.Errorf("a: %s, want the project's listing", got[1].SHA256)
	}
	if got[2].SHA256 != "" {
		t.Errorf("a/new is the run's own: %q", got[2].SHA256)
	}
	// The root's listing leaves out .gohard and the run's mine.txt.
	rootEs, _ := base.ReadDir(root)
	rootEs = slices.DeleteFunc(rootEs, func(e fs.DirEntry) bool {
		return e.Name() == env.Reserved
	})
	if got[0].SHA256 != env.ListingSHA256(rootEs) {
		t.Errorf(". listing hash includes the run's or GoHard's entries")
	}
	if len(o.Inputs()) != 0 {
		t.Errorf("a listing is not a file read: %v", o.Inputs())
	}
}

// TestListingSHA256: the hash depends on every name and on whether an
// entry is a directory or a link, and on nothing else.
func TestListingSHA256(t *testing.T) {
	base, root := listingProject(t)
	es, err := base.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	h := env.ListingSHA256(es)
	if h != env.ListingSHA256(slices.Clone(es)) {
		t.Fatal("not a function of the listing")
	}
	seen := map[string]bool{h: true}
	for i := range es {
		other := slices.Delete(slices.Clone(es), i, i+1)
		x := env.ListingSHA256(other)
		if seen[x] {
			t.Errorf("dropping %s does not change the hash", es[i].Name())
		}
		seen[x] = true
	}
	empty := env.ListingSHA256(nil)
	if empty == "" || seen[empty] {
		t.Error("the empty listing has no hash of its own")
	}
	// A file and a directory of the same name differ.
	f := fs.FileInfoToDirEntry(fileInfo{"n", false})
	d := fs.FileInfoToDirEntry(fileInfo{"n", true})
	if env.ListingSHA256([]fs.DirEntry{f}) ==
		env.ListingSHA256([]fs.DirEntry{d}) {
		t.Error("a file and a directory hash alike")
	}
}

// TestOverlayRel: Rel maps names to clean root-relative paths through
// the project's links and refuses names outside the sandbox.
func TestOverlayRel(t *testing.T) {
	base, root := listingProject(t)
	o := env.NewOverlay(base, root)
	for name, want := range map[string]string{
		".": ".", "a/./y/..": "a", "l/x.txt": "a/x.txt",
		filepath.Join(root, "a", "y"): "a/y", "new/dir": "new/dir",
		"../work/a": "a", // leaves the root and comes back
	} {
		if got, err := o.Rel(name); err != nil || got != want {
			t.Errorf("Rel(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"..", "../other", ".gohard",
		".gohard/run", "/etc", "a/../../x"} {
		if _, err := o.Rel(name); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("Rel(%q): %v, want permission denied", name, err)
		}
	}
}

// TestOverlayRemoveDir: removing a project directory needs it empty
// (as the run sees it), the root is never removed, and the project
// itself is never changed.
func TestOverlayRemoveDir(t *testing.T) {
	base, root := listingProject(t)
	o := env.NewOverlay(base, root)
	if err := o.Remove("a/y"); err == nil {
		t.Fatal("removed a non-empty project directory")
	}
	if err := o.Remove("a/y/z.txt"); err != nil {
		t.Fatal(err)
	}
	if err := o.Remove("a/y"); err != nil {
		t.Fatalf("empty (to the run) directory: %v", err)
	}
	if _, err := o.Stat("a/y"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a/y still there: %v", err)
	}
	if err := o.Remove("."); err == nil {
		t.Error("removed the root")
	}
	if _, err := base.Stat(filepath.FromSlash("/work/a/y/z.txt")); err != nil {
		t.Errorf("the project changed: %v", err)
	}
	if len(o.Files()) != 0 || len(o.Dirs()) != 0 {
		t.Errorf("a removal wrote %v %v", o.Files(), o.Dirs())
	}
}

type fileInfo struct {
	name string
	dir  bool
}

func (f fileInfo) Name() string { return f.name }
func (f fileInfo) Size() int64  { return 0 }
func (f fileInfo) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir
	}
	return 0
}
func (f fileInfo) ModTime() (t time.Time) { return t }
func (f fileInfo) IsDir() bool            { return f.dir }
func (f fileInfo) Sys() any               { return nil }
