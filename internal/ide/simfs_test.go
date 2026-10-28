package ide

import (
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
)

// TestReadOnce: an editor run reads each project file once; a rebuilt
// run on the same view sees exactly what the first read saw, even
// after the disk changed, a file it found missing stays missing, and
// the view never writes.
func TestReadOnce(t *testing.T) {
	sim := simenv.New(simenv.Options{Dir: "/work",
		Files: map[string][]byte{"in.txt": []byte("first")}})
	base := sim.FS
	r := newReadOnce(base)
	if b, err := r.ReadFile("/work/in.txt"); err != nil ||
		string(b) != "first" {
		t.Fatalf("read %q %v", b, err)
	}
	if _, err := r.ReadFile("/work/new.txt"); !errors.Is(err,
		fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	fi, err := r.Stat("/work/in.txt")
	if err != nil {
		t.Fatal(err)
	}
	es, err := r.ReadDir("/work")
	if err != nil || len(es) != 1 {
		t.Fatalf("readdir %v %v", es, err)
	}
	for name, data := range map[string]string{"/work/in.txt": "second!",
		"/work/new.txt": "n"} {
		if err := base.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := r.ReadFile("/work/in.txt")
	if err != nil || string(b) != "first" {
		t.Errorf("reread %q %v, want the first read", b, err)
	}
	b[0] = 'X' // the caller's copy
	if b, _ := r.ReadFile("/work/in.txt"); string(b) != "first" {
		t.Errorf("a caller changed the remembered read: %q", b)
	}
	if _, err := r.ReadFile("/work/new.txt"); !errors.Is(err,
		fs.ErrNotExist) {
		t.Errorf("a missing file appeared: %v", err)
	}
	if again, _ := r.Stat("/work/in.txt"); again.Size() != fi.Size() {
		t.Errorf("stat changed: %d, want %d", again.Size(), fi.Size())
	}
	if es, _ := r.ReadDir("/work"); len(es) != 1 {
		t.Errorf("listing changed: %v", es)
	}
	rc, err := r.Open("/work/in.txt")
	if err != nil {
		t.Fatal(err)
	}
	if all, _ := io.ReadAll(rc); string(all) != "first" {
		t.Errorf("open %q", all)
	}
	writes := []error{
		r.WriteFile("/work/w.txt", nil, 0o644),
		r.Rename("/work/in.txt", "/work/x.txt"),
		r.Remove("/work/in.txt"),
		r.MkdirAll("/work/d", 0o755),
		r.Chmod("/work/in.txt", 0o600),
	}
	_, err = r.Create("/work/c.txt")
	writes = append(writes, err)
	_, err = r.Append("/work/in.txt")
	writes = append(writes, err)
	_, err = r.WriteTemp("/work", "t", nil)
	writes = append(writes, err)
	for i, err := range writes {
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("write %d: %v", i, err)
		}
	}
	if b, _ := base.ReadFile("/work/in.txt"); string(b) != "second!" {
		t.Errorf("the view wrote the project: %q", b)
	}
}
