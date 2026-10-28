package main

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/stdlib"
)

// stdlibRepo is a repository with the stdlib package marker and fake
// GHDL and NVC source trees holding one package per source file.
func stdlibRepo(t *testing.T) (root string, tl *toolset) {
	t.Helper()
	root = t.TempDir()
	mk := func(rel, s string) {
		name := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, name, s)
	}
	mk(manifestPath, fakeManifest)
	mk("internal/stdlib/refset.go", "package stdlib\n")
	for _, s := range stdlib.RefSets {
		for i, f := range s.Files {
			mk(path.Join("src", s.Tool, f.Path), "package p"+
				strings.Repeat("x", i)+" is\n  constant C_"+s.Std.String()+
				" : integer := 1;\nend package;\n")
		}
	}
	tl = &toolset{found: map[string]*tool{}}
	for _, n := range []string{"ghdl", "nvc"} {
		tl.found[n] = &tool{name: n, version: "9.9.9",
			path: filepath.Join(root, "src", n),
			fake: func(string, []string) (string, int) { return "", 0 }}
	}
	return root, tl
}

// TestStdlibSection: --update records every reference set from the
// tool's sources with the oracle's provenance, the check accepts the
// recording, and a set not recorded by the section, or with another
// source list, fails the check. No corpus.yaml hash is written.
func TestStdlibSection(t *testing.T) {
	root, tl := stdlibRepo(t)
	var out bytes.Buffer
	sel := []*section{stdlibSection}
	if err := doUpdate(root, sel, nil, tl, &out); err != nil {
		t.Fatal(err)
	}
	for _, s := range stdlib.RefSets {
		b := readFile(t, root, path.Join(s.Dir(), s.File()))
		if !strings.HasPrefix(b, "# Declarations of "+
			strings.ToUpper(s.Tool)+" 9.9.9's VHDL-"+s.Std.String()) ||
			!strings.Contains(b, stdlib.RecordedBy) ||
			!strings.Contains(b, " C_"+s.Std.String()+"\n") {
			t.Errorf("%s:\n%s", s.Name(), b)
		}
	}
	if got := readFile(t, root, manifestPath); got != fakeManifest {
		t.Error("the stdlib section changed corpus.yaml")
	}
	probs, err := doCheck(root, sel, nil)
	if err != nil || len(probs) > 0 {
		t.Fatalf("check after update: %v %q", err, probs)
	}
	nvc := filepath.Join(root, "internal/stdlib/testdata/nvc/2008.txt")
	b, _ := os.ReadFile(nvc)
	write(t, nvc, strings.Replace(string(b), stdlib.RecordedBy,
		"# recorded by hand", 1))
	probs, _ = doCheck(root, sel, nil)
	if len(probs) != 1 || !strings.Contains(probs[0], "not recorded by") {
		t.Fatalf("hand-recorded set: %q", probs)
	}
	write(t, nvc, strings.Replace(string(b), "# source: std.08/env.vhd\n",
		"", 1))
	probs, _ = doCheck(root, sel, nil)
	if len(probs) != 1 || !strings.Contains(probs[0], "sources") {
		t.Fatalf("missing source: %q", probs)
	}
	os.Remove(nvc)
	probs, _ = doCheck(root, sel, nil)
	if len(probs) != 1 || !strings.Contains(probs[0], "is not recorded") {
		t.Fatalf("missing set: %q", probs)
	}
}

// TestStdlibRecordedSets: the committed sets are the oracle's (the
// real repository's check of this section).
func TestStdlibRecordedSets(t *testing.T) {
	probs, err := doCheck(repoRoot(t), []*section{stdlibSection}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probs {
		t.Error(p)
	}
	ts, _ := stdlibSection.targets(&ctx{root: repoRoot(t)})
	if len(ts) != len(stdlib.RefSets) {
		t.Fatalf("%d targets, want %d", len(ts), len(stdlib.RefSets))
	}
}

// TestNVCSourceVersion: NVC's sources are read from the submodule only
// when its configure.ac version is the installed nvc's.
func TestNVCSourceVersion(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, filepath.FromSlash(nvcSubmodule))
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	nvc := &tool{name: "nvc", version: "1.23.0"}
	if _, err := refSourceDir(root, nvc); err == nil ||
		!strings.Contains(err.Error(), "git submodule update --init") {
		t.Fatalf("missing submodule: %v", err)
	}
	write(t, filepath.Join(sub, "configure.ac"),
		"AC_INIT([nvc], [1.22.0],\n")
	if _, err := refSourceDir(root, nvc); err == nil ||
		!strings.Contains(err.Error(), "is NVC 1.22.0 but nvc is 1.23.0") {
		t.Fatalf("other version: %v", err)
	}
	write(t, filepath.Join(sub, "configure.ac"),
		"AC_INIT([nvc], [1.23.0],\n")
	dir, err := refSourceDir(root, nvc)
	if err != nil || dir != filepath.Join(sub, "lib") {
		t.Fatalf("same version: %q %v", dir, err)
	}
}
