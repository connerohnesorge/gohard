package waveform_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/trace"
	"github.com/connerohnesorge/gohard/internal/trace/fst"
	"github.com/connerohnesorge/gohard/internal/trace/tracetest"
	"github.com/connerohnesorge/gohard/internal/trace/waveform"
)

func prog(context.Context, env.Command) (env.Result, error) {
	return env.Result{}, nil
}

const name = "/work/.gohard/traces/tb-7.trace"

func world(programs ...string) *simenv.Sim {
	ps := map[string]simenv.Program{}
	for _, p := range programs {
		ps[p] = prog
	}
	return simenv.New(simenv.Options{
		Path:     []string{"/usr/bin", "/opt/bin"},
		Programs: ps,
		Files: map[string][]byte{
			name: trace.Encode(tracetest.Sample()),
		},
	})
}

func TestOpen(t *testing.T) {
	cases := []struct {
		name     string
		programs []string
		viewer   string
		path     string
	}{
		{"surfer", []string{"/usr/bin/surfer"}, "surfer",
			"/usr/bin/surfer"},
		{"both", []string{"/opt/bin/surfer", "/usr/bin/gtkwave"},
			"surfer", "/opt/bin/surfer"},
		{"gtkwave only", []string{"/opt/bin/gtkwave"}, "gtkwave",
			"/opt/bin/gtkwave"},
		{"neither", nil, "", ""},
	}
	want, _ := fst.Exporter{}.Export(tracetest.Sample())
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := world(c.programs...)
			e := w.Env()
			r, err := waveform.Open(e.FS, e.Executor, name)
			if err != nil {
				t.Fatal(err)
			}
			fstName := "/work/.gohard/traces/tb-7.fst"
			if r.FST != fstName || r.Viewer != c.viewer ||
				r.Path != c.path {
				t.Fatalf("result %+v", r)
			}
			got, err := e.FS.ReadFile(fstName)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("exported FST differs: %v", err)
			}
			var starts []env.Command
			for _, call := range w.Executor.Calls() {
				if !call.Start {
					t.Errorf("Run called: %+v", call.Command)
				}
				starts = append(starts, call.Command)
			}
			if c.viewer == "" {
				if len(starts) != 0 {
					t.Errorf("started %+v with no viewer", starts)
				}
				if r.Message() != "waveform written to "+fstName+
					" (no surfer or gtkwave on PATH)" {
					t.Errorf("message %q", r.Message())
				}
				return
			}
			if len(starts) != 1 || starts[0].Path != c.path ||
				!slices.Equal(starts[0].Args, []string{fstName}) {
				t.Errorf("starts = %+v", starts)
			}
			if r.Message() != "opened "+fstName+" in "+c.viewer {
				t.Errorf("message %q", r.Message())
			}
		})
	}
}

// failStart is an executor whose surfer is on PATH but cannot start.
type failStart struct{ *simenv.Executor }

func (f failStart) Start(cmd env.Command) error {
	if cmd.Path == "/usr/bin/surfer" {
		return errors.New("exec format error")
	}
	return f.Executor.Start(cmd)
}

func TestOpenFallsBack(t *testing.T) {
	w := world("/usr/bin/surfer", "/usr/bin/gtkwave")
	r, err := waveform.Open(w.Env().FS, failStart{w.Executor}, name)
	if err != nil || r.Viewer != "gtkwave" {
		t.Fatalf("result %+v, %v", r, err)
	}
}

func TestOpenErrors(t *testing.T) {
	w := world()
	e := w.Env()
	if _, err := waveform.Open(e.FS, e.Executor,
		"/work/missing.trace"); err == nil {
		t.Error("missing trace opened")
	}
	e.FS.WriteFile("/work/bad.trace", []byte("nope\n"), 0o644)
	if _, err := waveform.Open(e.FS, e.Executor,
		"/work/bad.trace"); err == nil {
		t.Error("invalid trace opened")
	}
	if len(w.Executor.Calls()) != 0 {
		t.Error("a viewer started for a bad trace")
	}
}
