package repro

import (
	"bytes"
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
