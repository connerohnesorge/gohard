package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/stdlib"
)

// The stdlib section (#33): the declaration sets of GHDL's and NVC's
// std/ieee sources (stdlib.RefSets), which the first-standard tags are
// derived from and the 2008 view is compared with (internal/stdlib
// TestGHDL2008Set, TestNVC2008Set, TestTags). GHDL's sources are read
// from the Nix shell's ghdl (lib/ghdl/src next to its bin). NVC ships
// its libraries compiled only, so its sources are read from the pinned
// NVC submodule (testdata/nvc/lib), whose version must be the Nix
// shell's nvc. The recorded sets feed no corpus.yaml hash: the
// internal/stdlib tests read them directly. The check verifies each
// recorded file's provenance and source list; the set comparisons are
// the internal/stdlib tests.

// nvcSubmodule is the pinned NVC checkout (the regression submodule).
const nvcSubmodule = "testdata/nvc"

var stdlibSection = &section{
	name: "stdlib",
	targets: func(x *ctx) ([]target, error) {
		// A repository without the stdlib package (the oracle's own
		// test repositories) has no reference sets.
		if _, err := os.Stat(filepath.Join(x.root, "internal", "stdlib",
			"refset.go")); err != nil {
			return nil, nil
		}
		var ts []target
		for _, s := range stdlib.RefSets {
			ts = append(ts, target{
				Ref:   entryRef{List: "stdlib", Name: s.Name()},
				Dir:   s.Dir(),
				Files: []string{s.File()},
			})
		}
		return ts, nil
	},
	record: func(x *ctx, tl *toolset, t target) ([]goldenFile, error) {
		s, _ := refSet(t.Ref.Name)
		tool, err := tl.get(s.Tool)
		if err != nil {
			return nil, err
		}
		dir, err := refSourceDir(x.root, tool)
		if err != nil {
			return nil, err
		}
		b, err := stdlib.RecordRefSet(s, tool.version,
			func(p string) ([]byte, error) {
				return os.ReadFile(filepath.Join(dir,
					filepath.FromSlash(p)))
			})
		if err != nil {
			return nil, err
		}
		return []goldenFile{{Name: s.File(), Data: b}}, nil
	},
	check: func(x *ctx, t target, golden []goldenFile) []string {
		s, _ := refSet(t.Ref.Name)
		return checkRefSet(s, golden[0].Data)
	},
}

func refSet(name string) (stdlib.RefSet, bool) {
	i := slices.IndexFunc(stdlib.RefSets, func(s stdlib.RefSet) bool {
		return s.Name() == name
	})
	if i < 0 {
		return stdlib.RefSet{}, false
	}
	return stdlib.RefSets[i], true
}

// checkRefSet checks a recorded set: provenance, header and the exact
// source list of s, and at least one declaration.
func checkRefSet(s stdlib.RefSet, b []byte) []string {
	var probs []string
	head := "# Declarations of " + strings.ToUpper(s.Tool) + " "
	if !strings.HasPrefix(string(b), head) ||
		!strings.Contains(string(b), "\n"+stdlib.RecordedBy+"\n") {
		probs = append(probs, path.Join(s.Dir(), s.File())+
			": not recorded by this section: "+rerun+" stdlib")
	}
	srcs, keys := stdlib.ParseRefSet(b)
	var want []string
	for _, f := range s.Files {
		want = append(want, f.Path)
	}
	if !slices.Equal(srcs, want) {
		probs = append(probs, fmt.Sprintf("%s: sources %v, want %v: %s "+
			"stdlib", s.File(), srcs, want, rerun))
	}
	if len(keys) == 0 {
		probs = append(probs, s.File()+": no declarations: "+rerun+
			" stdlib")
	}
	return probs
}

var acInit = regexp.MustCompile(`AC_INIT\(\[nvc\],\s*\[([^\]]+)\]`)

// refSourceDir returns the folder the tool's std/ieee sources are read
// from: GHDL's installed lib/ghdl/src, or the NVC submodule's lib when
// its version is the installed nvc's.
// A fake tool (tests) reads them from its path.
func refSourceDir(root string, t *tool) (string, error) {
	if t.fake != nil {
		return t.path, nil
	}
	switch t.name {
	case "ghdl":
		bin, err := filepath.EvalSymlinks(t.path)
		if err != nil {
			return "", err
		}
		dir := filepath.Join(filepath.Dir(filepath.Dir(bin)), "lib", "ghdl",
			"src")
		if _, err := os.Stat(dir); err != nil {
			return "", fmt.Errorf("ghdl sources: %w", err)
		}
		return dir, nil
	case "nvc":
		sub := filepath.Join(root, filepath.FromSlash(nvcSubmodule))
		ac, err := os.ReadFile(filepath.Join(sub, "configure.ac"))
		if err != nil {
			return "", fmt.Errorf("NVC sources: %w (run git submodule "+
				"update --init %s)", err, nvcSubmodule)
		}
		m := acInit.FindSubmatch(ac)
		if m == nil {
			return "", fmt.Errorf("%s/configure.ac: no AC_INIT version",
				nvcSubmodule)
		}
		if string(m[1]) != t.version {
			return "", fmt.Errorf("%s is NVC %s but nvc is %s: pin the "+
				"submodule to the shell's version", nvcSubmodule, m[1],
				t.version)
		}
		return filepath.Join(sub, "lib"), nil
	}
	return "", fmt.Errorf("no std/ieee sources for %s", t.name)
}
