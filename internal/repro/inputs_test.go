package repro

import (
	"bytes"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
)

// TestVerifyInputs (#18, #29): a replay whose input files differ from
// the recording is repro/source-changed listing them: a changed or
// removed input, and a file recorded as absent that now exists. The
// same inputs replay.
func TestVerifyInputs(t *testing.T) {
	files := map[string][]byte{
		"src/a.vhd":    []byte("entity a is end;"),
		"data/in.txt":  []byte("hello\n"),
		"data/two.txt": []byte("two\n"),
	}
	s := simenv.New(simenv.Options{Dir: "/proj", Files: files})
	fsys := s.Env().FS
	srcs, err := HashFiles(fsys, "/proj", []string{"src/a.vhd"})
	if err != nil {
		t.Fatal(err)
	}
	h := sample()
	h.Sources, h.Config = srcs, nil
	h.Inputs = InputFiles([]env.Input{
		{Path: "data/in.txt", SHA256: Hash(files["data/in.txt"])},
		{Path: "data/later.txt"},
		{Path: "data/two.txt", SHA256: Hash(files["data/two.txt"])},
	})
	if c, err := Verify(fsys, "/proj", h, h.Version); err != nil ||
		!c.OK() {
		t.Fatalf("unchanged inputs refused: %+v %v", c, err)
	}
	mustWrite := func(name, data string) {
		t.Helper()
		if err := fsys.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("/proj/data/in.txt", "changed\n")
	mustWrite("/proj/data/later.txt", "new\n")
	if err := fsys.Remove("/proj/data/two.txt"); err != nil {
		t.Fatal(err)
	}
	c, err := Verify(fsys, "/proj", h, h.Version)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"data/in.txt", "data/later.txt", "data/two.txt"}
	if c.OK() || !slices.Equal(c.Changed, want) {
		t.Fatalf("changed %v, want %v", c.Changed, want)
	}
	ds := c.Diagnostics("r.json")
	if len(ds) != 1 || ds[0].Code != diag.ReproSourceChanged ||
		!strings.Contains(ds[0].Message, "data/later.txt") {
		t.Errorf("diagnostics %+v", ds)
	}
}

// TestInputsEncoding: no inputs is no "inputs" key (a v1.0 file reads
// and writes back byte for byte); inputs are sorted, and an absent one
// keeps its empty hash through a round trip; bad inputs are invalid.
func TestInputsEncoding(t *testing.T) {
	h := sample()
	a, err := Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	h.Inputs = []File{}
	if b, _ := Marshal(h); !bytes.Equal(a, b) ||
		bytes.Contains(b, []byte(`"inputs"`)) {
		t.Errorf("empty inputs reached the bytes:\n%s", b)
	}
	h.Inputs = []File{{Path: "z.txt", SHA256: Hash(nil)},
		{Path: "a.txt"}}
	b, err := Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if want := []File{{Path: "a.txt"}, {Path: "z.txt",
		SHA256: Hash(nil)}}; !slices.Equal(back.Inputs, want) {
		t.Errorf("inputs %+v, want %+v", back.Inputs, want)
	}
	if h.Inputs[0].Path != "z.txt" {
		t.Error("Marshal reordered the caller's inputs")
	}
	if RunDir(h) != RunDir(sample()) {
		t.Error("the inputs changed the run directory")
	}
	for _, bad := range [][]File{
		{{Path: "../x.txt"}},
		{{Path: "/abs.txt"}},
		{{Path: "a.txt"}, {Path: "a.txt", SHA256: Hash(nil)}},
		{{Path: "a.txt", SHA256: "XYZ"}},
	} {
		h.Inputs = bad
		if _, err := Marshal(h); err == nil {
			t.Errorf("inputs %+v accepted", bad)
		}
	}
}

// TestVerifyListings (#18, #29): the directories a run listed (std.env
// DIR_*) are recorded with the hash of what the overlay saw, and a
// replay whose project listing differs, by an added, removed or
// retyped entry or a directory that appeared or went away, is
// repro/source-changed naming the directory. Property: over seeded
// random trees and edits, Verify flags exactly the listed directories
// whose entries changed, and accepts the unchanged tree.
func TestVerifyListings(t *testing.T) {
	for seed := range uint64(40) {
		r := &env.SplitMix64{State: seed}
		names := []string{"a.txt", "B.txt", "c", "d/e.txt", "d/f.txt",
			"d/g/h.txt", "x/y.txt"}
		files := map[string][]byte{"src/a.vhd": []byte("entity a is end;")}
		for _, n := range names {
			if env.Intn(r, 2) == 0 {
				files["p/"+n] = []byte(n)
			}
		}
		s := simenv.New(simenv.Options{Dir: "/proj", Files: files})
		fsys := s.Env().FS
		ov := env.NewOverlay(fsys, "/proj")
		dirs := []string{".", "p", "p/d", "p/d/g", "p/x", "p/none"}
		for _, d := range dirs {
			_, _ = ov.ReadDir(d)
		}
		h := sample()
		h.Sources, h.Config = nil, nil
		h.Listings = InputFiles(ov.Listings())
		exist := 0 // an absent directory is seen in its parent's listing
		for _, d := range dirs {
			if fi, err := fsys.Stat("/proj/" + d); err == nil && fi.IsDir() {
				exist++
			}
		}
		if len(h.Listings) != exist {
			t.Fatalf("seed %d: listings %+v", seed, h.Listings)
		}
		for _, l := range h.Listings {
			got, err := env.ProjectListing(fsys, "/proj", l.Path)
			if err != nil || got != l.SHA256 {
				t.Fatalf("seed %d: %s: overlay %q, project %q (%v)",
					seed, l.Path, l.SHA256, got, err)
			}
		}
		if c, err := Verify(fsys, "/proj", h, h.Version); err != nil ||
			!c.OK() {
			t.Fatalf("seed %d: unchanged tree refused: %+v %v", seed, c,
				err)
		}
		// One edit: add or remove a file below a random directory.
		n := "p/" + names[env.Intn(r, len(names))]
		want := map[string]bool{}
		if _, ok := files[n]; ok {
			if err := fsys.Remove("/proj/" + n); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := fsys.MkdirAll(path.Dir("/proj/"+n),
				0o755); err != nil {
				t.Fatal(err)
			}
			if err := fsys.WriteFile("/proj/"+n, []byte("new"),
				0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, l := range h.Listings {
			now, _ := env.ProjectListing(fsys, "/proj", l.Path)
			if now != l.SHA256 {
				want[l.Path] = true
			}
		}
		c, err := Verify(fsys, "/proj", h, h.Version)
		if err != nil {
			t.Fatal(err)
		}
		if len(want) == 0 {
			t.Fatalf("seed %d: edit of %s changed no listing", seed, n)
		}
		if len(c.Changed) != len(want) || c.OK() {
			t.Fatalf("seed %d: edit of %s: changed %v, want %v", seed, n,
				c.Changed, want)
		}
		for _, p := range c.Changed {
			if !want[p] {
				t.Fatalf("seed %d: %s flagged, want %v", seed, p, want)
			}
		}
	}
	h := sample()
	a, _ := Marshal(h)
	h.Listings = []File{}
	if b, _ := Marshal(h); !bytes.Equal(a, b) {
		t.Error("empty listings reached the bytes")
	}
	h.Listings = []File{{Path: "d", SHA256: Hash(nil)}, {Path: "."}}
	b, err := Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(b)
	if err != nil || len(back.Listings) != 2 ||
		back.Listings[0].Path != "." {
		t.Fatalf("round trip %+v %v", back.Listings, err)
	}
	if RunDir(h) != RunDir(sample()) {
		t.Error("the listings changed the run directory")
	}
	for _, bad := range [][]File{{{Path: "../x"}}, {{Path: "/abs"}},
		{{Path: "d"}, {Path: "d"}}} {
		h.Listings = bad
		if _, err := Marshal(h); err == nil {
			t.Errorf("listings %+v accepted", bad)
		}
	}
}
