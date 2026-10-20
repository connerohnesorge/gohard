package main

import (
	"fmt"
	"path"

	"github.com/connerohnesorge/gohard/internal/corpus"
	"github.com/connerohnesorge/gohard/internal/simrun"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// The golden section (#36): GoHard's own canonical trace of each
// simulating design at seed 0, golden/trace.txt, hashed into
// trace_sha256. It runs no external tool. It has targets only once
// the sequential interpreter is registered (simrun.DefaultCode);
// until then trace_sha256 stays pending and the corpus harness
// (go test ./internal/corpus) reports the designs as pending.

const fieldTrace = "trace_sha256"

var goldenSection = &section{
	name: "golden",
	targets: func(x *ctx) ([]target, error) {
		if simrun.DefaultCode == nil {
			return nil, nil
		}
		var ts []target
		for _, d := range x.man.Designs {
			if d.Expected.StopTime == "" || d.Expected.StopTime == "none" ||
				d.Milestone != "v1.0" {
				continue
			}
			ts = append(ts, target{
				Ref: entryRef{List: "designs", Name: d.Name,
					Field: fieldTrace, After: "oracle_sha256"},
				Dir:   path.Join(d.dir(), "golden"),
				Files: []string{"trace.txt"},
			})
		}
		return ts, nil
	},
	record: func(x *ctx, _ *toolset, t target) ([]goldenFile, error) {
		b, err := gohardTrace(x, t.Ref.Name)
		if err != nil {
			return nil, err
		}
		return []goldenFile{{Name: "trace.txt", Data: b}}, nil
	},
	check: func(x *ctx, t target, golden []goldenFile) []string {
		b, err := gohardTrace(x, t.Ref.Name)
		if err != nil {
			return []string{err.Error()}
		}
		if n, a, g, ok := trace.FirstDiff(golden[0].Data, b); ok {
			return []string{fmt.Sprintf("trace.txt line %d: golden %q, "+
				"GoHard %q: %s", n, a, g, rerun)}
		}
		return nil
	},
}

// gohardTrace runs the main run of a design at seed 0 and returns its
// canonical trace.
func gohardTrace(x *ctx, name string) ([]byte, error) {
	m, err := corpus.ParseManifest(x.src)
	if err != nil {
		return nil, err
	}
	d, _ := m.Design(name)
	r := d.Runs()[0]
	src, err := r.Sources(func(rel string) ([]byte, error) {
		return readRepo(x.root, rel)
	})
	if err != nil {
		return nil, err
	}
	res, err := corpus.Execute(r, src,
		corpus.Options{Code: simrun.DefaultCode})
	if err != nil {
		return nil, err
	}
	if res.Text == nil {
		return nil, fmt.Errorf("GoHard did not simulate %s: %v", name,
			res.Messages)
	}
	return res.Text, nil
}
