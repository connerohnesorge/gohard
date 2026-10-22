package main

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/connerohnesorge/gohard/internal/corpus"
)

// The conformance section (#18): the NVC cross-check of the
// conformance cases under testdata/conformance (corpus.ConformanceDir,
// format in its README). Its targets are the cases that have a
// <case>.nvc.txt; to add one, create the file empty and run
//
//	go run ./tools/oracle --update -design FEATURE/CASE conformance
//
// The record step runs NVC (corpus.ConformanceCase.NVCArgs) in a fresh
// folder holding only the case file and writes the combined output,
// exit code, NVC version and the source's sha256. The check, which
// runs in CI without NVC, requires the recording to be of the current
// source and command, and NVC's report and assertion messages (as a
// sorted multiset) and pass/fail to equal those of the case's .want,
// unless the case is pending or has an nvc-differs: header. The
// recordings feed no corpus.yaml hash: the source hash inside each
// recording is what makes it stale.

const conformanceList = "conformance"

var conformanceSection = &section{
	name: "conformance",
	targets: func(x *ctx) ([]target, error) {
		cases, err := conformanceCases(x.root)
		if err != nil {
			return nil, err
		}
		var ts []target
		for _, c := range cases {
			ts = append(ts, target{
				Ref: entryRef{List: conformanceList,
					Name: c.Feature + "/" + c.Name},
				Dir:   path.Join(corpus.ConformanceDir, c.Feature),
				Files: []string{c.NVCFile()},
			})
		}
		return ts, nil
	},
	record: func(x *ctx, tl *toolset, t target) ([]goldenFile, error) {
		c, src, err := conformanceCase(x.root, t)
		if err != nil {
			return nil, err
		}
		nvc, err := tl.get("nvc")
		if err != nil {
			return nil, err
		}
		w, err := newWorkDir(map[string][]byte{c.File(): src},
			[]string{c.File()})
		if err != nil {
			return nil, err
		}
		defer w.close()
		out, code, err := nvc.run(w.dir, c.NVCArgs()...)
		if err != nil {
			return nil, err
		}
		out = strings.ReplaceAll(out, w.dir, "$WORK")
		return []goldenFile{{Name: c.NVCFile(),
			Data: c.RenderNVC(nvc.version, src, code, out)}}, nil
	},
	check: func(x *ctx, t target, golden []goldenFile) []string {
		c, src, err := conformanceCase(x.root, t)
		if err != nil {
			return []string{err.Error()}
		}
		var want *corpus.Want
		if b, err := readRepo(x.root, path.Join(t.Dir,
			c.WantFile())); err == nil {
			w, err := corpus.ParseWant(b)
			if err != nil {
				return []string{c.WantFile() + ": " + err.Error()}
			}
			want = &w
		} else if c.Pending == "" {
			return []string{c.WantFile() + " is missing: run go test " +
				"./internal/cli -run TestConformance -update"}
		}
		var probs []string
		for _, p := range c.CheckNVC(src, golden[0].Data, want) {
			probs = append(probs, p+": "+rerun+" -design "+
				t.Ref.Name+" conformance")
		}
		return probs
	},
}

// conformanceCases lists the cases with an NVC recording, in folder
// and file name order.
func conformanceCases(root string) ([]corpus.ConformanceCase, error) {
	dir := filepath.Join(root, filepath.FromSlash(corpus.ConformanceDir))
	feats, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // the oracle's own test repositories
	}
	if err != nil {
		return nil, err
	}
	var out []corpus.ConformanceCase
	for _, f := range feats {
		if !f.IsDir() {
			continue
		}
		ents, err := os.ReadDir(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range ents {
			name, ok := strings.CutSuffix(e.Name(), ".nvc.txt")
			if !ok {
				continue
			}
			src, err := readRepo(root, path.Join(corpus.ConformanceDir,
				f.Name(), name+".vhd"))
			if err != nil {
				return nil, err
			}
			c, err := corpus.ParseConformanceCase(f.Name(), name, src)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
	}
	return out, nil
}

// conformanceCase reads the case of target t and its source.
func conformanceCase(root string, t target) (corpus.ConformanceCase,
	[]byte, error) {
	feature, name, _ := strings.Cut(t.Ref.Name, "/")
	src, err := readRepo(root, path.Join(t.Dir, name+".vhd"))
	if err != nil {
		return corpus.ConformanceCase{}, nil, err
	}
	c, err := corpus.ParseConformanceCase(feature, name, src)
	return c, src, err
}
