package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/corpus"
)

const confCase = "-- std: 2008\nentity c_tb is\nend entity;\n"

// confNVC stands in for NVC: it reports the case file's text after
// "--say " as a note, and fails without the file.
func confNVC() *toolset {
	fake := func(dir string, args []string) (string, int) {
		b, err := os.ReadFile(filepath.Join(dir, "c.vhd"))
		if err != nil || strings.Join(args, " ") !=
			"--std=2008 -a c.vhd -e c_tb -r" {
			return "bad run", 2
		}
		_, say, _ := strings.Cut(string(b), "--say ")
		say, _, _ = strings.Cut(say, "\n")
		return "** Note: 0ms+0: " + say + "\n   Process :c_tb at " +
			dir + "/c.vhd:1\n", 0
	}
	return &toolset{found: map[string]*tool{
		"nvc": {name: "nvc", version: "1.23.0", fake: fake},
	}}
}

// TestConformanceSection: only cases with a .nvc.txt are targets; an
// update records the run with $WORK for the run folder; the check
// catches a stale source and a message that differs from the .want.
func TestConformanceSection(t *testing.T) {
	root := fakeRepo(t, "entity a is\nend entity a;\n")
	dir := filepath.Join(root, filepath.FromSlash(corpus.ConformanceDir),
		"feat")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := corpus.ParseConformanceCase("feat", "c", []byte(confCase))
	if err != nil {
		t.Fatal(err)
	}
	src := confCase + "-- --say hi\n"
	write(t, filepath.Join(dir, "c.vhd"), src)
	write(t, filepath.Join(dir, "other.vhd"), confCase)
	want := func(msg string) string {
		return string(c.RenderWant(0, []byte("c.vhd:1:1: hint[sim/"+
			"assert-note]: report note: "+msg+"\n"), nil))
	}
	write(t, filepath.Join(dir, "c.want"), want("hi"))
	sel := []*section{conformanceSection}
	x, err := loadCtx(root)
	if err != nil {
		t.Fatal(err)
	}
	if ts, err := conformanceSection.targets(x); err != nil ||
		len(ts) != 0 {
		t.Fatalf("targets without recordings: %v, %v", ts, err)
	}
	write(t, filepath.Join(dir, "c.nvc.txt"), "")
	probs, err := doCheck(root, sel, nil)
	if err != nil || len(probs) != 1 {
		t.Fatalf("empty recording: %q, %v", probs, err)
	}
	var log bytes.Buffer
	if err := doUpdate(root, sel, nil, confNVC(), &log); err != nil {
		t.Fatal(err)
	}
	rec := readFile(t, root, corpus.ConformanceDir+"/feat/c.nvc.txt")
	if !strings.Contains(rec, "at $WORK/c.vhd:1\n") ||
		!strings.HasPrefix(rec, "# NVC output of testdata/conformance/"+
			"feat/c.vhd\n") {
		t.Fatalf("recording:\n%s", rec)
	}
	if probs, err := doCheck(root, sel, nil); err != nil ||
		len(probs) > 0 {
		t.Fatalf("after update: %q, %v", probs, err)
	}
	// GoHard's output says something else than NVC.
	write(t, filepath.Join(dir, "c.want"), want("ho"))
	probs, _ = doCheck(root, sel, nil)
	if len(probs) != 1 || !hasProblem(probs, "messages") {
		t.Fatalf("different message: %q", probs)
	}
	// The source changed after recording.
	write(t, filepath.Join(dir, "c.want"), want("hi"))
	write(t, filepath.Join(dir, "c.vhd"), src+"\n")
	probs, _ = doCheck(root, sel, nil)
	if len(probs) != 1 || !hasProblem(probs, "another version") {
		t.Fatalf("stale source: %q", probs)
	}
}
