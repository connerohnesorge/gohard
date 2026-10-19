package sema

import (
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
)

// TestParallelJobs runs the unit jobs on the real goroutine pool (run
// with -race in CI): the result equals the sequential one, so jobs of
// one level share no mutable state.
func TestParallelJobs(t *testing.T) {
	jobs := realenv.New(realenv.Options{}).Jobs
	for i, files := range designs {
		want := fingerprint(analyze(t, files, nil))
		for range 5 {
			r := Analyze(Input{Files: permute(files, 11),
				Config: config.Defaults(), Jobs: jobs, Limit: 16})
			if got := fingerprint(r); got != want {
				t.Fatalf("design %d: the real job pool changed the "+
					"result", i)
			}
		}
	}
	srcs := neorv32Sources(t, "../../testdata/neorv32")
	if len(srcs) == 0 {
		return
	}
	c := config.Defaults()
	c.Libraries = []config.Library{{Name: "neorv32",
		Folders: []config.Folder{{Path: "."}}}}
	seq := fingerprint(Analyze(Input{Files: srcs, Config: c}))
	par := fingerprint(Analyze(Input{Files: srcs, Config: c, Jobs: jobs,
		Limit: 16}))
	if seq != par {
		t.Fatal("NEORV32: the real job pool changed the result")
	}
}
